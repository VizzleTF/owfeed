# How to write documentation people actually read

*[Русская версия](writing-guide_ru.md)*

The rules below are what this project writes by. They come from research on how people read
technical text, not from taste. Sources are at the bottom.

One number to keep in mind: **79% of readers scan a page, 16% read it word by word**
(Nielsen Norman Group). Writing for the 16% loses the other 79%.

---

## Before you write

### Pick one of four jobs

Every page does exactly one of these. Mixing them is the most common reason documentation
becomes unreadable.

| Job | The reader is | Answers | Example |
|---|---|---|---|
| **Tutorial** | learning | "teach me" | Ship your first package |
| **How-to** | working | "I need to do X" | Add a package to the feed |
| **Reference** | looking something up | "what are the options" | Every command and flag |
| **Explanation** | trying to understand | "why is it like this" | Why there is no revocation |

If a page argues *and* instructs, split it. Instructions get skipped when the reader has to
wade through reasoning; reasoning gets skipped when the reader is mid-task.

### Write down who is reading and what they want

One sentence, before the first paragraph: *"Someone who has a built package and wants users to
install it."* If you cannot write that sentence, the page has no shape yet.

### Assume the reader landed here from a search engine

They did not read the previous page. Every page has to work alone: say what it is about in the
first two lines, and link to what it depends on.

---

## Structure

### Put the answer first

Conclusion, then detail. Not background, then build-up, then conclusion.

> **No:** Package managers differ between releases, and this has consequences for how indexes
> are signed, which means that a feed serving both lines needs two keys.
>
> **Yes:** A feed serving both release lines needs two signing keys — apk and opkg verify
> different schemes.

The same rule applies inside headings, table cells, and list items: the keyword goes first.

### One idea per paragraph

Readers skip a paragraph whose first few words do not interest them. A second idea buried in
the fourth line is an idea nobody reads.

Three to seven lines is the right length. A one-line paragraph is fine.

### Headings are a map, not a joke

The reader scans headings to decide where to stop. Write what the section is about, in plain
words, keyword first. Sentence case.

> **No:** Things that will bite you
>
> **Yes:** Common mistakes and how to avoid them

### Use lists and tables for anything with more than two parts

Steps → numbered list. Options, symptoms, comparisons → table. Prose that carries four facts
in one sentence is prose nobody parses.

### Keep the page short enough to hold one topic

If a page needs its own table of contents, it is probably two pages.

---

## Sentences and words

### Address the reader as "you", and use the active voice

> **No:** The key should then be placed in the repository's secrets, where it will be read by
> the workflow.
>
> **Yes:** Put the key in the repository's secrets. The workflow reads it from there.

### Put the condition before the instruction

The reader needs to know whether a sentence applies before they act on it.

> **No:** Run `owfeed lock --update` if upstream added an architecture.
>
> **Yes:** If upstream added an architecture, run `owfeed lock --update`.

### Cut half the words

Aim for half of what you would write in prose. Concrete cuts that always work:

| Cut | Keep |
|---|---|
| "in order to" | "to" |
| "it is worth noting that X" | "X" |
| "you may want to consider running" | "run" |
| "this is not a bug, it is a deliberate decision" | "this is deliberate" |
| "which is why", "that is the reason", "hence" | start a new sentence |

### Say the thing, do not perform it

Aphorisms, rhetorical questions, and clever contrasts read as noise to someone looking for an
answer. Objective wording alone measured 27% better in usability testing; combined with
brevity and scannability, 124%.

> **No:** A green report that means "nothing was looked at" is worse than no report.
>
> **Yes:** If a check cannot run, it fails. It is never reported as passed.

### Explain a term the first time, or do not use it

Jargon is invisible to the person who wrote it — that is what "expert blindness" means. For
every term specific to your domain, either define it in the sentence where it first appears, or
link to a glossary entry.

> **Yes:** `noarch` — apk's word for a package that runs on any architecture. opkg calls the
> same thing `all`.

### Avoid

- Exclamation marks and ALL CAPS for emphasis
- "simply", "just", "obviously", "of course" — if it were obvious, the page would not exist
- Negation stacked on negation ("it is not uncommon for X not to be present")
- Sentences over ~25 words
- Latin abbreviations: use "for example", not "e.g."

---

## Code and commands

### Every example is runnable and current

The reader will copy it. A command with a stale version tag or a flag that no longer exists
costs more trust than a missing example.

Check on every release: version tags in examples, flag names, file paths, URLs.

### Show the command, then say what it does

```sh
owfeed doctor          # checks the built tree before you publish it
```

Not three paragraphs of preamble followed by the command.

### Say what success looks like

After a step that can fail quietly, show the expected output. The reader needs to know whether
to continue.

### Errors get the same treatment as instructions

For each error a reader can hit: what it says, what caused it, what to do. In a table, not a
narrative.

---

## Maintenance

**Wrong documentation is worse than missing documentation.** A missing page sends the reader
elsewhere. A wrong page sends them down a path that does not work, and they trust the next page
less.

- Delete pages that are no longer true. Do not annotate them.
- One fact lives in one place. Two copies drift, and the reader cannot tell which is current.
- Link instead of repeating.
- Say the date or version something was true for, when it matters.

---

## The checklist

Before publishing, read the page and answer:

- [ ] Does it do exactly one of the four jobs?
- [ ] Does the first screen say what the page is and who it is for?
- [ ] Does every heading make sense read on its own, out of order?
- [ ] Is the answer before the reasoning?
- [ ] Have you cut half the words?
- [ ] Would every command run today, as written?
- [ ] Is every domain term defined or linked on first use?
- [ ] Read it aloud — does it sound like a person explaining, or a person arguing?

---

## Sources

- [Diátaxis](https://diataxis.fr/) — the four modes and why not to mix them
- [How Users Read on the Web](https://www.nngroup.com/articles/how-users-read-on-the-web/) and
  [Concise, Scannable, and Objective](https://www.nngroup.com/articles/concise-scannable-and-objective-how-to-write-for-the-web/),
  Nielsen Norman Group — the 79/16 split, the F-pattern, the 124% figure
- [Microsoft Writing Style Guide: scannable content](https://learn.microsoft.com/en-us/style-guide/scannable-content/)
  — front-loading, paragraph length, "get to the point, then stop"
- [Google developer documentation style guide](https://developers.google.com/style/highlights)
  — second person, active voice, conditions before instructions
- [GOV.UK content principles](https://www.gov.uk/government/publications/govuk-content-principles-conventions-and-research-background/govuk-content-principles-conventions-and-research-background)
  — plain English, front-loading, what to avoid
- [Federal plain language guidelines](https://digital.gov/guides/plain-language) — concrete
  words, short sentences, lists
- [Write the Docs: documentation principles](https://www.writethedocs.org/guide/writing/docs-principles/)
  — skimmable, current, unique
- Mark Baker, *Every Page is Page One* — every page is somebody's first page
- John Carroll, *The Nurnberg Funnel* — minimalism: short task-shaped chunks, action first
