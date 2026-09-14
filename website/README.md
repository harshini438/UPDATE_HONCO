# Honco Chat — public landing page

The product website for Honco Chat. **This is not the application.** It is a
static page that describes the product and sends people to the real Honco
Chat deployment through "Open Honco Chat":

```
PUBLIC WEBSITE  ->  "Open Honco Chat"  ->  EXISTING HONCO CHAT APPLICATION
```

Nothing in here touches the Mattermost server, the plugin, the database or
any integration. It can be built and hosted anywhere that serves static files.

## Run it

```bash
cd website
npm install          # one dev dependency: Vite
npm run dev          # http://localhost:5173, HONCO_CHAT_URL from .env.development
npm run build        # static output in dist/
npm run preview      # serve dist/ on http://localhost:4173
```

## Configuration

| Variable | Purpose | Default |
|---|---|---|
| `HONCO_CHAT_URL` | Where "Open Honco Chat" and "Sign In" (`/login`) point. | `http://localhost:8065` (development fallback only) |
| `HONCO_DOCS_URL` | Footer "Documentation" link. | unset → shown as plain text |
| `HONCO_CONTACT_URL` | Footer "Contact" link. | unset → shown as plain text |

Values are read at **build time** (Vite `envPrefix: ['HONCO_']`), from the
environment or from `.env.production` / `.env.local` (git-ignored). Example:

```bash
HONCO_CHAT_URL=https://chat.example.com npm run build
```

`.env.development` carries the localhost fallback for `npm run dev`; a
production build with `HONCO_CHAT_URL` set contains no `localhost` at all.

## Layout

```
website/
  index.html        the page (semantic sections, inline SVG icon sprite)
  src/styles.css    design tokens (light + dark), layout, mockups, motion
  src/main.js       link wiring from env, mobile menu, theme toggle, reveal
  public/favicon.svg
  vite.config.js    envPrefix HONCO_
  .env.example      documented variables
  .env.development  localhost fallback for `npm run dev`
```

## What is deliberately not on the page

No testimonials, customer logos, statistics, user counts, compliance badges
or third-party logos. The product preview, AI panels, meeting card and task
list are CSS illustrations labelled as such — no real conversation data and
no fabricated AI output. The AI section says the assistant works with the AI
service you connect; it does not claim a production AI service is live.
