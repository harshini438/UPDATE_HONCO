// Render selected PDF pages to PNG with pdf.js + Chrome (no native canvas needed): open the pdf in a page and draw via pdf.js in-browser.
const {chromium} = require('C:/Users/Dell/AppData/Local/Temp/honco-browser/node_modules/playwright-core');
const path = require('path'); const url = require('url'); const fs = require('fs');
(async () => {
    const b = await chromium.launch({executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true, args: ['--allow-file-access-from-files']});
    const p = await b.newPage({viewport: {width: 1000, height: 1400}});
    const pdfData = fs.readFileSync(path.resolve(__dirname, '..', (process.env.DOC === 'learning' ? 'HONCO_CHAT_PROJECT_LEARNING_GUIDE.pdf' : 'HONCO_CHAT_COMPLETE_DOCUMENTATION.pdf'))).toString('base64');
    await p.goto(url.pathToFileURL(path.resolve(__dirname, 'viewer.html')).href);
    for (const pg of (process.argv[2] || '3,9,12,20,30').split(',').map(Number)) {
        await p.evaluate(async ([data, pg]) => {
            const pdfjs = await import('./node_modules/pdfjs-dist/build/pdf.mjs');
            pdfjs.GlobalWorkerOptions.workerSrc = './node_modules/pdfjs-dist/build/pdf.worker.mjs';
            const doc = await pdfjs.getDocument({data: Uint8Array.from(atob(data), (c) => c.charCodeAt(0))}).promise;
            const page = await doc.getPage(pg); const vp = page.getViewport({scale: 1.6});
            const c = document.getElementById('c'); c.width = vp.width; c.height = vp.height;
            await page.render({canvasContext: c.getContext('2d'), viewport: vp}).promise;
        }, [pdfData, pg]);
        await p.locator('#c').screenshot({path: 'page-' + pg + '.png'});
    }
    await b.close(); console.log('ok');
})();
