# Contributing to tooti

Thank you for your interest.

## Workflow

1. Open an issue or discuss a significant change before large refactors.  
2. Fork the repository and create a branch from `main`.  
3. Keep commits focused; follow [Conventional Commits](https://www.conventionalcommits.org/) where practical (`feat:`, `fix:`, `docs:`, `chore:`, etc.).  
4. Run `go vet ./...` and `go test ./...` before opening a pull request.  
5. Open a PR against `main` with a clear description and any testing notes.

## Code style

- Match existing formatting (`gofmt` / `go fmt`).  
- Prefer small, reviewable changes.  
- Avoid unrelated drive-by edits in the same PR as a feature or fix.

## Security

Do not commit secrets, API keys, or wallet material. Report sensitive issues privately to the maintainers if a security contact is published on the repository; otherwise use GitHub private security advisories if enabled.
