# AGENTS.md

These instructions apply repo-wide.

1. Read `SPEC.md` and the `poc/` directory before making architecture decisions.
2. Build the production solution from the validated POC learnings; do not regress proven behavior such as end-of-session capture, SQLite persistence, git-based metadata enrichment, and post-hook reconciliation.
3. Use sub-agents for clearly separable work such as research, code review, and test/lint strategy, but keep implementation coordinated and coherent.
4. Treat code, tests, linting, build, installer/update flow, and documentation as first-class deliverables. None are optional.
5. Add and maintain a `Makefile` as the main entrypoint for common workflows. At minimum, provide targets for build, test, lint, and an all-up verification target.
6. Prefer a small, low-dependency implementation that matches the spec's install and packaging goals.
7. Keep changes incremental, production-oriented, and well-covered by tests. Add migrations, validation, and error handling where needed.
8. Update user and developer documentation alongside behavior changes, including install, hook setup, state layout, and reporting workflows.
9. Use the Multipass instance `default-workspace` for integration tests when that is the most realistic or practical environment.
10. Before finishing, run the relevant build, lint, and test flows through the Makefile and fix issues rather than documenting them as follow-up work.
11. If the spec and current code disagree, preserve validated behavior unless you intentionally update the spec and explain why in the repo.
