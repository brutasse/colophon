---
title: First page
---

The fragment below declares `fragment: true`, which keeps it out of the build
as a page of its own:

<!--colophon:include basics.md-->

A non-Markdown fragment ships as a highlighted fence when the include names its
`language`:

<!--colophon:include timeout.yaml language=yaml-->

> [!NOTE]
> **Shared warning**
>
> GitHub alerts render as alerts, and a bold first line is the title.

```bash
echo hello from a highlighted fence
```

| Name | Value |
| ---- | ----- |
| a    | 1     |

![Logo](../_static/logo.svg)

Raw HTML keeps an old anchor alive when a heading moves:

<div id="my-target"></div>

## Target section

Jump targets: [the page anchor](#target-section) and
[the explicit target](#my-target).
