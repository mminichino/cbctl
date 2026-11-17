# Getting started

1. **Connect.** Create an API key in the Capella UI (Organization settings → API Keys) and copy its
   **API Key Token** (shown once; not the API Key ID). Run `cbctl configure` and paste the token. The key is checked and stored in your OS keychain. For CI, set
   `CAPELLA_API_KEY` and `CAPELLA_ORG_ID` instead.
2. **Discover.** Run `cbctl capella sync` to write `capella.yaml` from your projects and clusters (or
   `cbctl capella init` to describe a new environment from a few answers). Nothing in Capella changes.
   cbctl maintains the file. For a new project: `cbctl capella project list`, then
   `cbctl capella project create NAME`.
3. **Validate** offline (optional): `cbctl capella validate`.
4. **Plan**: `cbctl capella plan`. Straight after discovery it reports no changes. Nothing changes.
5. **Deploy and change** with commands: `cbctl capella cluster options` shows what you can deploy (including
   the free tier, `--free-tier`); `cbctl capella cluster create NAME` (asks what it needs, deploys,
   waits), `cbctl capella cluster update NAME …`, `cbctl capella cluster scale NAME --nodes N`. Each shows the
   plan, asks once, records the change in capella.yaml and applies it.
6. **Sync**: `cbctl capella sync` refreshes capella.yaml after changes made in the Capella UI.

Next: `cbctl capella guide idempotency`, `cbctl capella guide scaling`, `cbctl capella guide safety`.
