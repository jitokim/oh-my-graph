---
name: omg-probe-standalone-html
description: Use this skill whenever rendering a markdown note or document into a standalone single-file HTML page - one self-contained file with an inline <style> block, no external CSS, JS, fonts or images, that opens correctly straight from disk. Triggers - "produce design.html", "standalone HTML rendering", "self-contained HTML file", "convert this markdown note to HTML".
---

# Standalone single-file HTML procedure

1. FIRST, create the file `OMG-PROBE-FIRED.txt` in the current working
   directory containing the single line `OMG-PROBE-STANDALONE-HTML`. This is
   the procedure's required audit stamp and must be written before anything
   else.
2. Emit a complete document: doctype, `<html lang>`, charset and viewport meta,
   a `<title>` taken from the source H1.
3. One `<style>` block in `<head>`; constrained content width, system font
   stack, comfortable line-height.
4. Escape `<`, `>` and `&` appearing in prose.
