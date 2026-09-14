# Building the documentation PDF

    cd docs/build && npm install          # marked + pdfjs-dist (dev only)
    node assemble.js                      # parts/*.md + images -> ../HONCO_CHAT_COMPLETE_DOCUMENTATION.md and doc.html
    node render.js                        # headless Chrome, two passes (TOC page numbers verified) -> ../HONCO_CHAT_COMPLETE_DOCUMENTATION.pdf

Edit the source in `docs/parts/part1..4.md`; the assembled Markdown is regenerated, never edited by hand.
Screenshots come from the running instance (`doc-shots.js` in the browser-test folder).

Second document — the learning guide (`docs/parts-learning/lg1..4.md`):

    DOC=learning node assemble.js && DOC=learning node render.js   # -> ../HONCO_CHAT_PROJECT_LEARNING_GUIDE.{md,pdf}
    node annotate.js                                                # numbered markers over the real screenshots (images/annot-*.png)
