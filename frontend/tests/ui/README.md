# Responsive v2 UI checks

Run from `frontend/`:

```sh
npx playwright install chromium webkit
npm run test:ui
```

The suite starts only Vite on `127.0.0.1:4100` and intercepts `/api/*` with
v2 fixtures. It does not start a backend, authenticate to a real workspace,
or change any database. Keep port 4100 free before running it.

Coverage includes desktop Chrome, Android Chrome and iPhone WebKit; mobile
navigation; task and note detail dialogs; calendar agenda/editing; capture
submission and feedback; keyboard focus; failure handling; 320px layouts;
and long forms in a short viewport. Screenshots are in `test-results/`.
The short viewport check does not emulate a real device's software keyboard.

For unit tests on Node 26, disable Node's experimental web storage so jsdom
can provide browser storage:

```sh
NODE_OPTIONS=--no-experimental-webstorage npm run test:unit
```

Real login, mobile synchronization and iPhone Simulator readiness must still
be checked against the canonical v2 runtime described in `AGENTS.md`.
