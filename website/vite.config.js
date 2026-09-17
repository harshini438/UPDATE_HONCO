import {defineConfig} from 'vite';

// The public website: a landing page plus the auth pages (login, signup,
// password reset, email verification) that talk to the real Honco Chat
// (Mattermost) API on the same origin behind the local reverse proxy.
//
// HONCO_CHAT_URL (empty for the same-origin proxy) is the only thing that
// changes between environments; see .env.example.
export default defineConfig({
    envPrefix: ['HONCO_'],
    build: {
        target: 'es2018',
        cssMinify: true,
        rollupOptions: {
            input: {
                index: 'index.html',
                login: 'login.html',
                signup: 'signup.html',
                forgot: 'forgot-password.html',
                reset: 'reset-password.html',
                verify: 'verify-email.html',
            },
        },
    },
});
