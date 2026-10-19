# Broken test site

Deliberately broken links, which `build --check` must report:

* [Good link](one.md)
* [Missing page](nowhere.md)
* [Built page via .html](one.html)
* [Missing anchor](one.md#nope)
* [Root-absolute page link](/one.md)
* [Absolute link to this site](https://docs.example.test/one.html)

The math on [the other page](two.md) has no `math: true` front matter.

<iframe src="https://embed.example.com/doc/1" title="No host configured"></iframe>
