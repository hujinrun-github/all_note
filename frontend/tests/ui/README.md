# Responsive v2 UI checks

Run from `frontend/`:

```sh
npx playwright install chromium webkit
npm run test:ui
```

Run the complete frontend release regression (lint, unit tests, production
build and all browser projects):

```sh
npm run test:regression
```

The button-by-button manual checklist and canonical regression user are
documented in
[`docs/frontend-web-regression-checklist.md`](../../../docs/frontend-web-regression-checklist.md).

The suite starts only Vite on `127.0.0.1:4100` and intercepts `/api/*` with
v2 fixtures. It does not start a backend, authenticate to a real workspace,
or change any database. Keep port 4100 free before running it.

Coverage includes desktop Chrome, Android Chrome and iPhone WebKit; mobile
navigation; task and note detail dialogs; calendar agenda/editing; capture
submission and feedback; keyboard focus; failure handling; 320px layouts;
and long forms in a short viewport. Screenshots are in `test-results/`.
The short viewport check does not emulate a real device's software keyboard.

The unit-test scripts disable Node's experimental web storage for Vitest and
its workers so jsdom can provide browser storage consistently on Node 24 and
Node 26. When invoking Vitest directly, use:

```sh
NODE_OPTIONS=--no-experimental-webstorage npx vitest run
```

Real login, mobile synchronization and iPhone Simulator readiness must still
be checked against the canonical v2 runtime described in `AGENTS.md`.
