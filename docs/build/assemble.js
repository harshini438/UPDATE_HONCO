// Joins docs/parts/*.md into HONCO_CHAT_COMPLETE_DOCUMENTATION.md, inserting the real
// screenshots at the sections they illustrate, and renders the HTML used for the PDF.
const fs = require('fs');
const path = require('path');
const {marked} = require('marked');

const DOCS = path.resolve(__dirname, '..');
const parts = ['part1.md', 'part2.md', 'part3.md', 'part4.md'].map((f) => fs.readFileSync(path.join(DOCS, 'parts', f), 'utf8'));
let md = parts.join('\n');

// Screenshots: [anchor text that starts a line, image, caption]. Inserted immediately before the anchor.
const shots = [
    ['## Table of contents', '02-workspace.png', 'Figure 0 — Honco Chat main workspace (Town Square) with a meeting card and a support card, 1440 px, light theme.'],
    ['### 5.1 Honco Workspace', '01-login.png', 'Figure 1 — Login page (Honco wordmark; the vendor branding is gone).'],
    ['### 5.3 Tasks tab', '03b-tasks-panel.png', 'Figure 2 — Honco Workspace panel, Tasks tab: filters, New task, rows with assignee avatar, status selector, Edit/Delete, paging.'],
    ['### 5.4 Meetings tab', '03c-task-form.png', 'Figure 3 — The task form (Title, Description, Assignee, Status, Due date, Save task / Cancel).'],
    ['### 5.5 AI Assistant tab', '06b-summary-failed.png', 'Figure 4 — Meetings tab (Meeting Intelligence) with the one real generation attempt: failed because the summarizer host is unreachable; "Try again" offered.'],
    ['### 5.6 Support tab', '07-ai-assistant.png', 'Figure 5 — AI Assistant tab for an ended meeting with no session: the honest empty state ("Honco AI did not join this meeting"), Reconnect, meeting selector.'],
    ['### 5.7 Search tab', '08-support.png', 'Figure 6 — Support tab as a requester (Request Support, own requests).'],
    ['### 5.8 Admin tab', '09-search.png', 'Figure 7 — Search tab: query "meeting" grouped by category.'],
    ['### 5.9 Meeting card buttons', '11-admin.png', 'Figure 8 — Admin tab (system admin): System health (Jitsi probe reporting "not reachable from this host"), Usage, Files, Notifications, AI Assistant, Security, Recent failures.'],
    ['### 5.10 Support card', '04-meeting-card.png', 'Figure 9 — A meeting card in the channel (Started by + avatar, status line, AI Assistant; Join Meeting appears while the meeting is not ended).'],
    ['### 5.11 Profile', '08b-support-card.png', 'Figure 10 — A support card in the channel (requester avatar, status dot, issue, agent).'],
    ['### 5.12 `/meet`', '10-profile-photo.png', 'Figure 11 — Profile → Profile Settings → Profile Photo, expanded (current photo, help text, Change photo / Save photo / Cancel).'],
    ['# 6. Task management', '12-mobile-channel.png', 'Figure 12 — Phone width (390 px): the channel view with cards; the panel is reached from the channel "≡" menu.'],
    ['# 9. Meeting Intelligence', '06c-summary-empty.png', 'Figure 13 — Meeting Intelligence "empty" result: no messages were posted during that meeting.'],
];
for (const [anchor, img, caption] of shots) {
    const i = md.indexOf('\n' + anchor);
    if (i < 0) { throw new Error('anchor not found: ' + anchor); }
    const block = `\n<figure class="shot"><img src="images/${img}" alt="${caption.replace(/"/g, '&quot;')}"><figcaption>${caption}</figcaption></figure>\n`;
    md = md.slice(0, i) + block + md.slice(i);
}
fs.writeFileSync(path.join(DOCS, 'HONCO_CHAT_COMPLETE_DOCUMENTATION.md'), md);

// ---- HTML for the PDF -------------------------------------------------------
marked.setOptions({gfm: true, breaks: false});
let body = marked.parse(md).split('src="images/').join('src="../images/');
// Numbered section ids for the TOC, page breaks before each numbered section.
let n = 0;
body = body.replace(/<h1(?: id="[^"]*")?>(\d+)\. /g, (m, num) => { n++; return `<h1 id="sec-${num}" class="section">${num}. `; });
// Build the TOC from the h1/h2 list in the markdown (the "Table of contents" list is replaced with real links).
const tocItems = [];
md.replace(/```[\s\S]*?```/g, '').replace(/^# (\d+)\. (.+)$/gm, (m, num, title) => { tocItems.push([num, title]); return m; });
const toc = '<nav class="toc"><h2>Table of contents</h2><ol>' + tocItems.map(([num, t]) => `<li><a href="#sec-${num}"><span class="t">${num}. ${t}</span><span class="dots"></span><span class="pg"></span></a></li>`).join('') + '</ol></nav>';
body = body.replace(/<h2(?: id="[^"]*")?>Table of contents<\/h2>\s*<ol>[\s\S]*?<\/ol>/, toc);

const css = fs.readFileSync(path.join(__dirname, 'doc.css'), 'utf8');
const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>Honco Chat — Complete System Documentation & User Guide</title>
<style>${css}</style>

</head><body><main>${body}</main></body></html>`;
fs.writeFileSync(path.join(__dirname, 'doc.html'), html);
console.log('markdown', md.length, 'chars; html', html.length, 'chars; toc entries', tocItems.length);
