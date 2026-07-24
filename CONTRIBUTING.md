# Contributing to LogLens

LogLens uses a protected-branch workflow:

1. Branch from `dev` using `feat/`, `fix/`, `docs/`, `test/`, `refactor/`, or
   `chore/`.
2. Keep commits focused and use Conventional Commits.
3. Open a pull request into `dev`; do not push feature work directly.
4. Update tests, documentation, and `api/openapi.yaml` when behavior changes.
5. Squash-merge after required checks pass and delete the feature branch.

Release metadata is prepared on `chore/release-vX.Y.Z`, reviewed into `dev`,
and then moved from `dev` to `main` in a dedicated release pull request. After
that pull request is merged, tag the merge commit as `vX.Y.Z` and publish the
corresponding GitHub Release.

## Local checks

```sh
make check
```

Never commit `.env`, credentials, private log files, generated exports,
benchmarks containing production data, or `.local/` product notes.

