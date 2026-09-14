// Renders doc.html to the PDF with headless Chrome, in two passes so the table of
// contents carries real page numbers: pass 1 lays the document out, pdf.js reads
// which page each numbered section starts on, pass 2 renders with those numbers.
const {chromium} = require('C:/Users/Dell/AppData/Local/Temp/honco-browser/node_modules/playwright-core');
const path = require('path');
const fs = require('fs');
const url = require('url');

const HTML = path.resolve(__dirname, 'doc.html');
const OUT = path.resolve(__dirname, '..', 'HONCO_CHAT_COMPLETE_DOCUMENTATION.pdf');
const TMP = path.resolve(__dirname, 'pass1.pdf');
const footer = `<div style="font:8pt 'Segoe UI',Arial,sans-serif;color:#666;width:100%;padding:0 17mm;display:flex;justify-content:space-between;">
  <span>Honco Chat — Complete System Documentation &amp; User Guide</span><span>Page <span class="pageNumber"></span> of <span class="totalPages"></span></span></div>`;
const header = `<div style="font:8pt 'Segoe UI',Arial,sans-serif;color:#888;width:100%;padding:0 17mm;text-align:right;">v1.0 · 14 Sep 2026 · commit 126acc9</div>`;

async function render(page, out) {
    await page.pdf({path: out, format: 'A4', printBackground: true, preferCSSPageSize: false, displayHeaderFooter: true, headerTemplate: header, footerTemplate: footer,
        margin: {top: '16mm', bottom: '16mm', left: '15mm', right: '15mm'}});
}
async function pageOfSections(pdfPath, titles) {
    const pdfjs = await import('pdfjs-dist/legacy/build/pdf.mjs');
    const doc = await pdfjs.getDocument({data: new Uint8Array(fs.readFileSync(pdfPath)), useSystemFonts: true}).promise;
    const found = {};
    for (let i = 1; i <= doc.numPages; i++) {
        const tc = await (await doc.getPage(i)).getTextContent();
        const lines = tc.items.map((it) => it.str).join(' ').replace(/\s+/g, ' ');
        for (const [num, title] of titles) {
            if (found[num]) { continue; }
            const needle = num + '. ' + title;
            // the TOC page itself also contains the titles; skip pages that contain "Table of contents"
            if (lines.includes(needle) && !/Table of contents/.test(lines)) { found[num] = i; }
        }
    }
    return {found, pages: doc.numPages};
}

(async () => {
    let html = fs.readFileSync(HTML, 'utf8');
    const titles = [...html.matchAll(/<h1 id="sec-(\d+)" class="section">(\d+)\. ([^<]+)<\/h1>/g)].map((m) => [m[1], m[3].replace(/&amp;/g, '&').replace(/&quot;/g, '"').split(' — ')[0]]);
    const b = await chromium.launch({executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true});
    const p = await b.newPage();
    const errs = []; p.on('pageerror', (e) => errs.push(e.message)); p.on('requestfailed', (r) => errs.push('missing ' + r.url()));
    await p.goto(url.pathToFileURL(HTML).href, {waitUntil: 'load', timeout: 120000});
    await p.emulateMedia({media: 'print'});
    await p.waitForTimeout(1500);
    await render(p, TMP);
    const {found, pages} = await pageOfSections(TMP, titles);
    console.log('pass 1:', pages, 'pages; sections located:', Object.keys(found).length, 'of', titles.length);
    // inject numbers into the TOC
    await p.evaluate((found) => { for (const [num, pg] of Object.entries(found)) { const el = document.querySelector('nav.toc a[href="#sec-' + num + '"] .pg'); if (el) { el.textContent = String(pg); } } }, found);
    await p.waitForTimeout(500);
    await render(p, OUT);
    const check = await pageOfSections(OUT, titles);
    const drift = Object.keys(found).filter((k) => found[k] !== check.found[k]);
    console.log('pass 2:', check.pages, 'pages;', drift.length ? 'DRIFT on sections ' + drift.join(',') : 'TOC page numbers verified');
    console.log(errs.length ? 'page errors: ' + errs.join(' | ') : 'no page errors');
    console.log('written', OUT, fs.statSync(OUT).size, 'bytes');
    fs.unlinkSync(TMP);
    await b.close();
})().catch((e) => { console.error('CRASH', e); process.exit(1); });
