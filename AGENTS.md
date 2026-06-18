# AGENTS.md

Instructions for AI agents working on this repository.

## Core Principles

**Read first**: `SPEC.md` defines the product requirements and architecture.

**Quality standards**: Code, tests, linting, documentation, and build tooling are all first-class deliverables. None are optional.

**Incremental delivery**: Make changes in small, well-tested increments. Run `make check` before considering work complete.

## Architecture Constraints

- **Small and focused**: Minimize dependencies. Pure-Go implementation with no cgo.
- **Validated behavior**: Do not regress proven patterns from POC (session-end capture, SQLite persistence, git-based metadata, async reconciliation).
- **Makefile-driven**: Use `Makefile` for all common workflows (build, test, lint, fmt, check).

## Development Workflow

1. **Before changing behavior**: Read relevant code and tests to understand current implementation.
2. **Make changes**: Update code, tests, and documentation together.
3. **Verify quality**: Run `make check` (builds, tests, lints, formats).
4. **Integration test**: For hook or end-to-end changes, test in Multipass instance `default-workspace`.
5. **Update docs**: Keep README.md and SPEC.md in sync with implementation.

## Key Files

- **SPEC.md**: Product specification and feature roadmap
- **README.md**: User-facing documentation
- **Makefile**: Build and verification entrypoint
- **cmd/arok/**: CLI entrypoint
- **internal/**: All implementation packages
- **poc/**: Original proof-of-concept (reference only, not used in production)

## Adding Harness Support

When adding support for a new AI agent tool (harness), read **`docs/adding-a-harness.md`**
before writing any code. It explains the key concepts, the expected file structure, and
important edge cases (thin hook payloads, async metrics, provisional capture). The existing
copilot harness (`internal/copilot/`, `internal/cli/app_copilot.go`) is the canonical
reference implementation to follow.

## Testing

- Unit tests: `go test ./...`
- Integration tests: Use Multipass instance `default-workspace` for realistic hook validation
- All tests must pass before merging

## Release Process

1. Create branch for changes
2. Implement, test, document
3. Open PR and merge to `main`
4. Tag release: `git tag -a v0.x.y -m "Release v0.x.y"`
5. Push tag: `git push origin v0.x.y`
6. GitHub Actions builds and publishes release binaries automatically

## Constraints

- Preserve backward compatibility with existing state directories and databases
- Schema changes require migration code
- Breaking changes require major version bump and clear upgrade path
