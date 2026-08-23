# Failure fixtures

- `build-failure` references a missing file and must produce `BUILDING → FAILED`.
- `crash` exits immediately and must fail Kubernetes rollout verification.
- `unhealthy` serves traffic but has no `/health` resource, so the readiness probe must fail and Forge must report `VERIFYING → FAILED`.

These directories are deterministic fixtures. The full API E2E script accepts a public GitHub repository URL because Forge deliberately rejects local/file repository URLs.

Run `make kind-up && make failure-tests` from the repository root. The script expects the invalid build to fail, pushes the two valid-but-broken runtime images to the local registry, and verifies through Forge's Kubernetes adapter that neither rollout is reported ready.
