// Honco Chat landing page. No framework: the page is static, and this file
// does four small things -- point the "Open Honco Chat" links at the
// configured application, the mobile menu, the theme toggle and the
// scroll-in reveal. Nothing here talks to the Honco Chat server.

// Where the application lives. Production sets HONCO_CHAT_URL at build
// time; the localhost fallback is for local development only.
// Same-origin by default: the website and Honco Chat are served behind one
// local reverse proxy, so links are relative (""). A production build can
// still point elsewhere by setting HONCO_CHAT_URL.
const CHAT_URL = (import.meta.env.HONCO_CHAT_URL ?? '').replace(/\/+$/, '');
const ENV_LINKS = {
    HONCO_DOCS_URL: import.meta.env.HONCO_DOCS_URL || '',
    HONCO_CONTACT_URL: import.meta.env.HONCO_CONTACT_URL || '',
};

// If Honco Chat bounced an unauthenticated visitor to the landing page with
// a ?redirect_to (its default is the site root, which is this website), send
// them to the sign-in page and remember where they were headed.
(function () {
    try {
        var rt = new URLSearchParams(window.location.search).get('redirect_to');
        if (rt) { window.location.replace('/login?redirect_to=' + encodeURIComponent(rt)); }
    } catch (e) { /* ignore */ }
})();

const doc = document.documentElement;
const $ = (sel, root = document) => root.querySelector(sel);
const $$ = (sel, root = document) => Array.from(root.querySelectorAll(sel));

// --- "Open Honco Chat" / "Sign In" --------------------------------------
for (const a of $$('[data-chat-link]')) {
    a.href = CHAT_URL + (a.dataset.chatLink || '');
    a.rel = 'noopener';
}
// Optional footer links: plain text when the deployment has not configured them.
for (const a of $$('[data-env-link]')) {
    const url = ENV_LINKS[a.dataset.envLink];
    if (url) {
        a.href = url;
        a.rel = 'noopener';
    } else {
        const span = document.createElement('span');
        span.textContent = a.textContent;
        span.title = 'Not configured for this deployment';
        a.replaceWith(span);
    }
}
$('#year').textContent = String(new Date().getFullYear());

// --- theme ---------------------------------------------------------------
const themeBtn = $('#theme-toggle');
const systemDark = window.matchMedia('(prefers-color-scheme: dark)');
const isDark = () => {
    const t = doc.getAttribute('data-theme');
    return t ? t === 'dark' : systemDark.matches;
};
const paintThemeButton = () => {
    const dark = isDark();
    themeBtn.setAttribute('aria-pressed', String(dark));
    themeBtn.setAttribute('aria-label', dark ? 'Switch to light mode' : 'Switch to dark mode');
};
themeBtn.addEventListener('click', () => {
    const next = isDark() ? 'light' : 'dark';
    doc.setAttribute('data-theme', next);
    try { localStorage.setItem('honco-theme', next); } catch (e) { /* private mode */ }
    paintThemeButton();
});
systemDark.addEventListener('change', paintThemeButton);
paintThemeButton();

// --- navbar --------------------------------------------------------------
const nav = $('.nav');
const navToggle = $('#nav-toggle');
const navLinks = $('#nav-links');
const setMenu = (open) => {
    doc.classList.toggle('nav-open-menu', open);
    navToggle.setAttribute('aria-expanded', String(open));
    navToggle.setAttribute('aria-label', open ? 'Close menu' : 'Open menu');
};
navToggle.addEventListener('click', () => setMenu(!doc.classList.contains('nav-open-menu')));
navLinks.addEventListener('click', (e) => { if (e.target.closest('a')) { setMenu(false); } });
document.addEventListener('keydown', (e) => {
    if (e.key === 'Escape' && doc.classList.contains('nav-open-menu')) { setMenu(false); navToggle.focus(); }
});
document.addEventListener('click', (e) => {
    if (doc.classList.contains('nav-open-menu') && !e.target.closest('.nav')) { setMenu(false); }
});
window.matchMedia('(min-width: 901px)').addEventListener('change', (m) => { if (m.matches) { setMenu(false); } });

const onScroll = () => nav.classList.toggle('scrolled', window.scrollY > 8);
window.addEventListener('scroll', onScroll, {passive: true});
onScroll();

// --- reveal on scroll ----------------------------------------------------
const reduced = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
const revealed = $$('.reveal');
if (reduced || !('IntersectionObserver' in window)) {
    revealed.forEach((el) => el.classList.add('in'));
} else {
    const io = new IntersectionObserver((entries) => {
        for (const en of entries) {
            if (en.isIntersecting) { en.target.classList.add('in'); io.unobserve(en.target); }
        }
    }, {rootMargin: '0px 0px -8% 0px', threshold: 0.08});
    revealed.forEach((el) => io.observe(el));
}
