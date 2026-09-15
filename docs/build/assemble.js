// Joins docs/parts/*.md into HONCO_CHAT_COMPLETE_DOCUMENTATION.md, inserting the real
// screenshots at the sections they illustrate, and renders the HTML used for the PDF.
const fs = require('fs');
const path = require('path');
const {marked} = require('marked');

const DOCS = path.resolve(__dirname, '..');
const DOC = process.env.DOC || 'reference';
const CFGS = {
    learning: {dir: 'parts-learning', files: ['lg1.md', 'lg2.md', 'lg3.md', 'lg4.md'], out: 'HONCO_CHAT_PROJECT_LEARNING_GUIDE', title: 'Honco Chat — Complete Project Learning & Developer Guide'},
    manual: {dir: 'parts-manual', files: ['bm1.md', 'bm2.md', 'bm3.md', 'bm4.md'], out: 'HONCO_CHAT_BUTTON_MANUAL', title: 'Honco Chat — Complete Button-by-Button User & Developer Manual'},
    reference: {dir: 'parts', files: ['part1.md', 'part2.md', 'part3.md', 'part4.md'], out: 'HONCO_CHAT_COMPLETE_DOCUMENTATION', title: 'Honco Chat — Complete System Documentation & User Guide'},
};
const CFG = CFGS[DOC] || CFGS.reference;
const parts = CFG.files.map((f) => fs.readFileSync(path.join(DOCS, CFG.dir, f), 'utf8'));
let md = parts.join('\n');

// Screenshots: [anchor text that starts a line, image, caption]. Inserted immediately before the anchor.
const SHOTS_MANUAL = [
    [
        "## Table of contents",
        "annot-workspace.png",
        "Figure 1 — The running Honco Chat, annotated. ① team menu ② Find channel ③ channel list ④ channel header ⑤ account menu (Profile) ⑥ Honco Workspace icon ⑦ AI Assistant icon ⑧ a meeting card ⑨ message composer."
    ],
    [
        "# 3. Button-by-button manual",
        "annot-tasks.png",
        "Figure 2 — Tasks tab, annotated. ① panel tabs ② All statuses ③ Mine ④ New task ⑤ status selector ⑥ Edit ⑦ Delete ⑧ due date / Overdue ⑨ Previous / Next."
    ],
    [
        "### BUTTON: Join Meeting (meeting card)",
        "bm-card-active.png",
        "Figure 3 — An active meeting card created during this verification pass: Join Meeting and AI Assistant are offered; View Summary is absent because this meeting has no usable summary."
    ],
    [
        "### BUTTON: Generate summary / Regenerate / Try again",
        "bm-summary-failed.png",
        "Figure 4 — Verified today: a forced generation on a meeting with four real messages ended in \"The summarizer is not reachable from this server.\", with Try again offered."
    ],
    [
        "### BUTTON: Start AI session / Start new AI session",
        "bm-ai-state.png",
        "Figure 5 — The AI Assistant tab on a live meeting with no AI service configured: status \"Ready\", Reconnect, the meeting selector and the introduction. No Start control is rendered."
    ],
    [
        "### BUTTON: Request Support (opens the form)",
        "bm-support-verified.png",
        "Figure 6 — The Support tab after creating a request during the verification pass."
    ],
    [
        "### BUTTON: Search + Enter, and the category chips",
        "bm-search-verified.png",
        "Figure 7 — The Search tab: the category chips carry a data hook, and selecting \"Tasks\" produced type=tasks on the wire."
    ],
    [
        "### BUTTON: Admin tab · Refresh",
        "annot-admin.png",
        "Figure 8 — The Admin tab, annotated. ① System health (live probes) ② Usage ③ Files ④ Refresh."
    ],
    [
        "### BUTTONS: Change photo · Save photo · Remove photo · Cancel (Profile Photo)",
        "annot-profile.png",
        "Figure 9 — Profile Settings, annotated. ① Profile Settings ② Profile Photo section ③ current photo ④ accepted types and size limit ⑤ Change photo ⑥ Save photo ⑦ Cancel."
    ],
    [
        "# 5. Profile & profile photo",
        "01-login.png",
        "Figure 10 — The login screen of the running application."
    ],
    [
        "# 10. Jibri / recording",
        "04-meeting-card.png",
        "Figure 11 — A meeting card after the meeting ended, as posted by the honco bot."
    ],
    [
        "# 14. Files & attachments",
        "08b-support-card.png",
        "Figure 12 — A support card in the channel: requester avatar, status, issue and the assigned agent."
    ],
    [
        "# 17. Admin dashboard",
        "12-mobile-channel.png",
        "Figure 13 — Phone width (390 px): the same channel with its cards; the panel is reached from the channel menu."
    ]
];
const shots = DOC === 'manual' ? SHOTS_MANUAL : DOC === 'learning' ? [
    ['## Table of contents', '01-login.png', 'Figure 0 — The login page of the running Honco Chat (the vendor branding is replaced by the Honco wordmark).'],
    ['# 11. Meeting Intelligence', '07-ai-assistant.png', 'Figure 6 — AI Assistant tab for an ended meeting with no session: the honest empty state, Reconnect, meeting selector, AI Meeting Summary block.'],
    ['# 12. AI Assistant', '06b-summary-failed.png', 'Figure 7 — Meeting Intelligence today: the real generation attempt failed because the summarizer host is unreachable; Try again is offered.'],
    ['# 14. Files', '08-support.png', 'Figure 8 — The Support tab as a requester (Request Support, own requests).'],
    ['# 17. Admin dashboard', '09-search.png', 'Figure 9 — The Search tab with results grouped by category.'],
    ['# 19. Database', '12-mobile-channel.png', 'Figure 10 — Phone width (390 px): the channel view with cards; the panel is reached from the channel menu.'],
    ['# 20. API concept', '06c-summary-empty.png', 'Figure 11 — Meeting Intelligence "empty" result: no messages were posted during that meeting, so there is nothing to summarise.'],
] : [
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
fs.writeFileSync(path.join(DOCS, CFG.out + '.md'), md);

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
const html = `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>${CFG.title}</title>
<style>${css}</style>

</head><body><main>${body}</main></body></html>`;
fs.writeFileSync(path.join(__dirname, DOC + '.html'), html);
console.log('markdown', md.length, 'chars; html', html.length, 'chars; toc entries', tocItems.length);
