# Building the documentation PDF

    cd docs/build && npm install          # marked + pdfjs-dist (dev only)
    node assemble.js                      # parts/*.md + images -> ../HONCO_CHAT_COMPLETE_DOCUMENTATION.md and doc.html
    node render.js                        # headless Chrome, two passes (TOC page numbers verified) -> ../HONCO_CHAT_COMPLETE_DOCUMENTATION.pdf

Edit the source in `docs/parts/part1..4.md`; the assembled Markdown is regenerated, never edited by hand.
Screenshots come from the running instance (`doc-shots.js` in the browser-test folder).
