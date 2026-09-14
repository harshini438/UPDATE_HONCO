import {defineConfig} from 'vite';

// The landing page is a static site. The only thing that changes between
// environments is where "Open Honco Chat" points, so that comes from the
// environment (see .env.example) rather than from the source.
export default defineConfig({
    envPrefix: ['HONCO_'],
    build: {
        target: 'es2018',
        cssMinify: true,
    },
});
