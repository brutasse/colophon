---
title: Embedded frames
---

The sanitizer keeps an `<iframe>` whose host is listed in the config's
`iframe-hosts`, and strips the `src` of any other host:

<iframe src="https://embed.example.com/doc/1" title="Allowed embed" width="600" height="400" loading="lazy"></iframe>

<iframe src="https://not-allowed.example.net/doc/2" title="Denied embed"></iframe>
