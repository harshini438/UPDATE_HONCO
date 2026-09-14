// Draws numbered markers over the real screenshots in docs/images (nothing in the image is altered
// except the overlaid badges). Positions are in pixels of the source image.
const {chromium} = require('C:/Users/Dell/AppData/Local/Temp/honco-browser/node_modules/playwright-core');
const path = require('path'); const fs = require('fs'); const url = require('url');
const IMG = path.resolve(__dirname, '..', 'images');
const jobs = [
    {src: '02-workspace.png', out: 'annot-workspace.png', marks: [[60, 60, 1], [120, 112, 2], [130, 250, 3], [620, 60, 4], [1416, 60, 5], [1417, 112, 6], [1417, 150, 7], [600, 620, 8], [600, 805, 9]]},
    {src: '03b-tasks-panel.png', out: 'annot-tasks.png', marks: [[36, 74, 1], [60, 116, 2], [126, 116, 3], [330, 116, 4], [60, 327, 5], [128, 327, 6], [165, 327, 7], [244, 297, 8], [48, 831, 9]]},
    {src: '04-meeting-card.png', out: 'annot-meeting-card.png', marks: [[14, 24, 1], [14, 50, 2], [14, 78, 3], [140, 112, 4]]},
    {src: '11-admin.png', out: 'annot-admin.png', marks: [[40, 158, 1], [40, 373, 2], [40, 680, 3], [340, 116, 4]]},
    {src: '10-profile-photo.png', out: 'annot-profile.png', marks: [[425, 154, 1], [585, 575, 2], [754, 655, 3], [660, 748, 4], [757, 795, 5], [890, 795, 6], [998, 795, 7]]},
];
(async () => {
    const b = await chromium.launch({executablePath: 'C:/Program Files/Google/Chrome/Application/chrome.exe', headless: true});
    for (const j of jobs) {
        const data = 'data:image/png;base64,' + fs.readFileSync(path.join(IMG, j.src)).toString('base64');
        const p = await b.newPage({viewport: {width: 1600, height: 1200}, deviceScaleFactor: 1});
        const html = `<style>body{margin:0}#w{position:relative;display:inline-block}img{display:block}.m{position:absolute;width:26px;height:26px;margin:-13px 0 0 -13px;border-radius:50%;background:#d4351c;color:#fff;font:bold 15px/26px Arial,sans-serif;text-align:center;border:2px solid #fff;box-shadow:0 0 0 2px #d4351c,0 1px 4px rgba(0,0,0,.5)}</style>
        <div id="w"><img id="i" src="${data}">${j.marks.map(([x, y, n]) => `<div class="m" style="left:${x}px;top:${y}px">${n}</div>`).join('')}</div>`;
        await p.setContent(html); await p.waitForTimeout(400);
        await p.locator('#w').screenshot({path: path.join(IMG, j.out)});
        await p.close(); console.log('wrote', j.out);
    }
    await b.close();
})();
