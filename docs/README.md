# Documentation

What Agentifi is and how to run it is in the [top-level README](../README.md);
the rules for changing it are in [`AGENTS.md`](../AGENTS.md), with
[`development.md`](development.md) and [`CONTRIBUTING.md`](../CONTRIBUTING.md).

## The application

- [`architecture.md`](architecture.md) — how the backend and the frontend are
  put together: the packages and what each owns, the route registry and
  in-process dispatch, the pure calculation core, the browsers, background
  jobs and migrations.
- [`calculations.md`](calculations.md) — the specification of every derived
  number. Tests take their expected values from it.
- [`data-model.md`](data-model.md) — the entities, their invariants, and what
  is materialized versus derived.
- [`assistant.md`](assistant.md) — the optional assistant: what it can do,
  how its proposals become changes, and what only a person does.

## Getting data in

- [`importing.md`](importing.md) — moving a dataset out of Simplifi, relinking
  imported accounts to SimpleFIN, and CSV and OFX/QFX files.
- [`connectors/`](connectors/README.md) — SimpleFIN bank sync, the bill
  providers, the Amazon and Costco connectors, and the browsers they use.

## Working on it

- [`AGENTS.md`](../AGENTS.md) — the ground rules and traps, a map of where
  each concern lives, and the commands to build, test and run.
- [`development.md`](development.md) — how new code is
  structured (an API resource, a connector, an assistant tool, a migration, a
  screen), and testing.
- [`connectors/adding-a-bill-provider.md`](connectors/adding-a-bill-provider.md)
  — adding a bill provider, step by step.

## Running it

- [`operations.md`](operations.md) — installing, upgrading, backups and
  restore, secrets and HTTPS.
- [`configuration.md`](configuration.md) — every setting, with its default.
