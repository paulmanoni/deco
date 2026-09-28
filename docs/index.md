---
layout: home

hero:
  name: deco
  text: Python-style decorators for Go
  tagline: Annotate any function or method with a doc comment — every caller transparently flows through your decorators. Plain Go, generated code, zero dependencies.
  actions:
    - theme: brand
      text: Get started
      link: /guide/getting-started
    - theme: alt
      text: What is deco?
      link: /guide/
    - theme: alt
      text: GitHub
      link: https://github.com/paulmanoni/deco

features:
  - icon: ✏️
    title: One comment, wired everywhere
    details: '//deco:wrap logged above a function or method wraps it for every caller. Stacks compose bottom-up, exactly like Python.'
  - icon: 🧰
    title: A go wrapper, not a new toolchain
    details: deco build, deco run, deco test — the real go toolchain runs underneath with a source overlay, so flags, exit codes and output are exactly go's, and your files are never modified.
  - icon: 📍
    title: Diagnostics point at your source
    details: Compiler, vet and test positions are remapped from the transpiled overlay back to your files, so a finding reports the same file:line as bare go.
  - icon: ⚡
    title: Fused middleware chains
    details: Write decorators as middleware factories and a whole stack runs with no reflection — ~52 ns for a 3-deep chain instead of ~900 ns.
  - icon: 📦
    title: Seamless with libraries
    details: A decorator from another package or an external module resolves automatically — go get is enough. Signatures are checked at transpile time.
  - icon: '0️⃣'
    title: Zero dependencies
    details: The module is stdlib-only, and generated code depends only on what you already import.
---
