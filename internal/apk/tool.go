package apk

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

// KeyRef marks an argument that names a signing key, so the runner can substitute the
// path the key is reachable at in whatever environment apk actually executes in.
//
// Signing keys are never staged into the working directory. The working directory is
// the feed's publish tree, and a key that lands there is one `git add -A` or one CI
// artifact upload away from being published — which is precisely the footgun in
// openwrt/gh-action-sdk, whose PRIVATE_KEY input writes the secret to private-key.pem
// inside the build tree that callers routinely upload.
func KeyRef(name string) string { return keyRefPrefix + name }

const keyRefPrefix = "\x00key:"

// Tool is a resolved apk-tools 3.x installation.
type Tool struct {
	// Version is what `apk --version` reported, e.g. "apk-tools 3.0.5".
	Version string
	// Origin describes where the binary came from, for `owfeed doctor` output.
	Origin string

	run runner
}

// Invocation is one apk command.
type Invocation struct {
	// Workdir becomes the process's working directory. mkndx in particular must run
	// with the publish directory as cwd and ./*.apk as arguments, or absolute path
	// prefixes leak into the index and every download URL it implies is wrong.
	Workdir string
	// KeyDir holds signing keys referenced by KeyRef. Mounted read-only; may be empty.
	KeyDir string
	Args   []string
}

// Result is the outcome of one apk command. ExitCode is reported separately from err
// because apk's exit codes are not uniformly meaningful — adbsign prints an error and
// exits 0 without doing anything (see docs/apk-behaviour.md), so callers that depend
// on an operation having happened must verify the artifact, not the status.
type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

type runner interface {
	exec(ctx context.Context, inv Invocation) (Result, error)
	describe() string
}

var ErrNoAPK = errors.New("apk: no usable apk-tools 3.x available")

// Run executes one apk command.
func (t *Tool) Run(ctx context.Context, inv Invocation) (Result, error) {
	return t.run.exec(ctx, inv)
}

// RunOK executes one apk command and turns a non-zero exit into an error carrying
// apk's own diagnostics, which are on stderr and are usually the only useful thing.
func (t *Tool) RunOK(ctx context.Context, inv Invocation) (Result, error) {
	res, err := t.Run(ctx, inv)
	if err != nil {
		return res, err
	}
	if res.ExitCode != 0 {
		return res, fmt.Errorf("apk %s: exit %d: %s",
			strings.Join(redactKeyRefs(inv.Args), " "), res.ExitCode, strings.TrimSpace(res.Stderr))
	}
	return res, nil
}

func redactKeyRefs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, keyRefPrefix) {
			out[i] = "<key:" + strings.TrimPrefix(a, keyRefPrefix) + ">"
			continue
		}
		out[i] = a
	}
	return out
}

// Options control how a Tool is resolved.
type Options struct {
	// Explicit is a path to an apk binary, from --apk or $OWFEED_APK. If set, it is
	// used as-is and nothing else is attempted.
	Explicit string
	// CacheRoot is where extracted SDK toolchains live, e.g. ~/.cache/owfeed.
	CacheRoot string
	// SDKDir is an already-extracted SDK cache directory (from Acquire).
	SDKDir string
	// AllowContainer permits falling back to Docker. Required on darwin, where the
	// SDK's .apk.bin is a Linux ELF and there is nothing to run it with natively.
	AllowContainer bool
	// ContainerImage is the image used for the container runner. It only has to be
	// able to exec a binary; the glibc loader and libc come from the SDK extraction,
	// so a musl-only image works.
	ContainerImage string
}

const defaultContainerImage = "alpine:3"

// Resolve picks an apk to run, in the order documented in the design: an explicit
// path, then a system apk if it is apk-tools 3.x, then the extracted SDK toolchain,
// natively where possible and through a container otherwise.
//
// The SDK toolchain is preferred over a system apk that is merely present because the
// generation that writes an index should match the generation that reads it on the
// device; a newer host apk can emit something the target cannot parse, and that
// failure does not surface until a user's `apk update` breaks.
func Resolve(ctx context.Context, opts Options) (*Tool, error) {
	if opts.Explicit != "" {
		t := &Tool{run: &nativeRunner{bin: opts.Explicit}, Origin: "explicit: " + opts.Explicit}
		if err := t.probe(ctx); err != nil {
			return nil, err
		}
		return t, nil
	}

	if opts.SDKDir != "" {
		if runtime.GOOS == "linux" && runtime.GOARCH == "amd64" {
			t := &Tool{run: newSDKNativeRunner(opts.SDKDir), Origin: "SDK: " + opts.SDKDir}
			if err := t.probe(ctx); err == nil {
				return t, nil
			}
			// Fall through to the container: a native run can fail for reasons the
			// container path does not share (no exec on the cache filesystem, for one).
		}
		if opts.AllowContainer {
			img := opts.ContainerImage
			if img == "" {
				img = defaultContainerImage
			}
			t := &Tool{run: &containerRunner{sdkDir: opts.SDKDir, image: img}, Origin: "SDK via container: " + img}
			if err := t.probe(ctx); err != nil {
				return nil, err
			}
			return t, nil
		}
		return nil, fmt.Errorf("%w: the SDK toolchain is a Linux x86-64 binary and this is %s/%s; "+
			"a container is required here but was not permitted", ErrNoAPK, runtime.GOOS, runtime.GOARCH)
	}

	if path, err := exec.LookPath("apk"); err == nil {
		t := &Tool{run: &nativeRunner{bin: path}, Origin: "PATH: " + path}
		if perr := t.probe(ctx); perr == nil {
			return t, nil
		}
	}

	return nil, ErrNoAPK
}

// probe runs `apk --version` and records the result, rejecting anything that is not
// apk-tools 3.x. Alpine's apk 2.x parses the same command line for some verbs and
// would produce a v2 index that OpenWrt 25.12 cannot read.
func (t *Tool) probe(ctx context.Context) error {
	res, err := t.Run(ctx, Invocation{Args: []string{"--version"}})
	if err != nil {
		return err
	}
	out := strings.TrimSpace(res.Stdout + res.Stderr)
	if res.ExitCode != 0 {
		return fmt.Errorf("%w: %s: exit %d: %s", ErrNoAPK, t.run.describe(), res.ExitCode, out)
	}
	if !strings.HasPrefix(out, "apk-tools 3.") {
		return fmt.Errorf("%w: %s reported %q, need apk-tools 3.x", ErrNoAPK, t.run.describe(), out)
	}
	t.Version = strings.SplitN(out, ",", 2)[0]
	return nil
}

// nativeRunner executes an apk binary directly.
type nativeRunner struct {
	bin string
	// argv0 lets the SDK variant prepend the loader invocation.
	argv0 []string
}

func newSDKNativeRunner(sdkDir string) *nativeRunner {
	return &nativeRunner{
		// Invoke the loader directly rather than through the SDK's bin/apk wrapper.
		// The wrapper is a bash script; going straight to the loader means bash is not
		// a requirement, which matters in minimal CI images.
		argv0: []string{
			filepath.Join(sdkDir, loaderPath),
			"--library-path", filepath.Join(sdkDir, libDirPath),
			filepath.Join(sdkDir, binPath),
		},
	}
}

func (r *nativeRunner) describe() string {
	if len(r.argv0) > 0 {
		return r.argv0[len(r.argv0)-1]
	}
	return r.bin
}

func (r *nativeRunner) exec(ctx context.Context, inv Invocation) (Result, error) {
	argv := r.argv0
	if len(argv) == 0 {
		argv = []string{r.bin}
	}
	args, err := resolveKeyRefs(inv.Args, inv.KeyDir)
	if err != nil {
		return Result{}, err
	}

	cmd := exec.CommandContext(ctx, argv[0], append(append([]string{}, argv[1:]...), args...)...)
	cmd.Dir = inv.Workdir
	return capture(cmd)
}

// containerRunner executes the extracted SDK toolchain inside a container. The SDK
// tree carries its own loader and libc, so the image only has to be able to exec.
type containerRunner struct {
	sdkDir string
	image  string
}

func (r *containerRunner) describe() string { return "docker " + r.image }

const (
	ctrSDK  = "/opt/owfeed-apk"
	ctrWork = "/work"
	ctrKeys = "/keys"
)

func (r *containerRunner) exec(ctx context.Context, inv Invocation) (Result, error) {
	args, err := resolveKeyRefs(inv.Args, ctrKeys)
	if err != nil {
		return Result{}, err
	}

	docker := []string{
		"run", "--rm",
		// The SDK toolchain is x86-64; on Apple silicon this selects emulation.
		"--platform", "linux/amd64",
		"-v", r.sdkDir + ":" + ctrSDK + ":ro",
	}
	if inv.Workdir != "" {
		abs, err := filepath.Abs(inv.Workdir)
		if err != nil {
			return Result{}, err
		}
		docker = append(docker, "-v", abs+":"+ctrWork, "-w", ctrWork)
	}
	if inv.KeyDir != "" {
		abs, err := filepath.Abs(inv.KeyDir)
		if err != nil {
			return Result{}, err
		}
		docker = append(docker, "-v", abs+":"+ctrKeys+":ro")
	}
	docker = append(docker, r.image,
		ctrSDK+"/"+loaderPath, "--library-path", ctrSDK+"/"+libDirPath, ctrSDK+"/"+binPath)
	docker = append(docker, args...)

	return capture(exec.CommandContext(ctx, "docker", docker...))
}

// resolveKeyRefs replaces KeyRef placeholders with paths under keyDir.
func resolveKeyRefs(args []string, keyDir string) ([]string, error) {
	out := make([]string, len(args))
	for i, a := range args {
		name, ok := strings.CutPrefix(a, keyRefPrefix)
		if !ok {
			out[i] = a
			continue
		}
		if keyDir == "" {
			return nil, fmt.Errorf("apk: argument references key %q but no key directory was provided", name)
		}
		out[i] = filepath.Join(keyDir, name)
	}
	return out, nil
}

func capture(cmd *exec.Cmd) (Result, error) {
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// A stray inherited LD_PRELOAD would be applied to the SDK loader invocation.
	cmd.Env = append(os.Environ(), "LD_PRELOAD=")

	err := cmd.Run()
	res := Result{Stdout: stdout.String(), Stderr: stderr.String()}

	var ee *exec.ExitError
	if errors.As(err, &ee) {
		res.ExitCode = ee.ExitCode()
		return res, nil
	}
	if err != nil {
		return res, err
	}
	return res, nil
}
