# Contributing to NexusGuard AI

First off, thank you for considering contributing to NexusGuard AI! It's people like you that make this tool great.

## Code of Conduct

This project and everyone participating in it is governed by a standard of respect and professionalism. Be kind, be constructive.

## How Can I Contribute?

### Reporting Bugs

- Check if the bug has already been reported
- Open a new issue with a clear title and description
- Include steps to reproduce, expected behavior, and actual behavior
- Include your environment details (OS, Go version, etc.)

### Suggesting Enhancements

- Open an issue with the tag `enhancement`
- Describe the feature and its use case
- Explain why it would be useful

### Pull Requests

1. Fork the repo and create your branch from `main`
2. If you've added code, add tests
3. Ensure all tests pass: `go test ./...`
4. Make sure your code follows Go conventions: `go fmt ./...`
5. Update documentation if needed
6. Submit the pull request!

## Development Setup

```bash
git clone https://github.com/smilespoon/nexusguard-ai.git
cd nexusguard-ai
go mod tidy
go test ./...
```

## Style Guide

- Follow standard Go conventions
- Use `go fmt` and `go vet`
- Add comments for exported functions
- Keep functions focused and small
- Use meaningful variable names

## Commit Messages

Use conventional commits:
- `feat:` New feature
- `fix:` Bug fix
- `docs:` Documentation
- `style:` Formatting
- `refactor:` Code restructuring
- `test:` Tests
- `chore:` Maintenance

---

**Author:** Mustafa Al-Aqrawi (Smile Spoon)
