# AROK implementation kickoff prompt

You are implementing the first production version of `arok` in this repository.

Start by reading:

- `AGENTS.md`
- `SPEC.md`
- everything under `poc/`

Context:

- The POC is validated and contains important learned behavior that must not be lost.
- The goal is to turn the spec + POC into a real, installable product with strong engineering quality.
- Keep the implementation small and low-dependency, aligned with the spec's binary/installer direction.

Critical behavior to preserve from the POC:

1. session-end driven capture for Copilot
2. SQLite as the canonical local store
3. git-based metadata enrichment from cwd
4. upsert/update behavior for resumed sessions with the same session ID
5. autonomous post-hook reconciliation when authoritative shutdown metrics arrive after the hook returns

Delivery expectations:

1. production-quality code
2. proper test suites
3. linting and formatting
4. build flow
5. documentation
6. a `Makefile` as the main entrypoint for common workflows

At minimum, the Makefile should provide:

- `build`
- `test`
- `lint`
- `fmt`
- `check`

Working style:

- Use sub-agents where it helps keep the main context clean, especially for:
  - repo/spec/POC exploration
  - test/lint/build strategy
  - code review
- Keep implementation coherent: one main thread should own the actual build-out.
- Prefer incremental delivery with working checkpoints rather than a huge unstructured rewrite.

Execution context:

- The Multipass instance `default-workspace` is available and may be used for integration tests when needed, especially for validating hook behavior, shared mounted state, and end-to-end Copilot flows.

What I want you to do:

1. Inspect the repo and choose the implementation approach that best satisfies the spec's packaging and dependency goals.
2. Create the initial project structure for the real product.
3. Implement the first end-to-end vertical slice for Copilot support.
4. Add installer/update flow aligned with the spec.
5. Add the SQLite schema, ingestion path, reconciliation path, and query/reporting foundation.
6. Add tests, linting, docs, and Makefile support as first-class work, not as cleanup.
7. Update documentation continuously as the implementation takes shape.

Constraints:

- Do not regress validated POC behavior without explicitly updating the spec/docs and explaining why.
- Do not treat tests, linting, docs, or build as optional.
- Do not leave the repo in a half-wired state.

Execution:

1. Begin by summarizing the implementation plan you will follow.
2. Then start executing it immediately.
3. Use sub-agents deliberately to reduce context pollution, but keep momentum on implementation.
