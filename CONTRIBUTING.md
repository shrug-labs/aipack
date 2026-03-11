# Contributing to aipack

We welcome your contributions! There are several ways you can help:

* [Report bugs and request features](https://github.com/shrug-labs/aipack/issues)
* [Review open pull requests](https://github.com/shrug-labs/aipack/pulls)
* Submit code changes

## Opening Issues

For bugs, include steps to reproduce the problem and what you expected to happen.
For feature requests, explain the use case and why the feature would be valuable.

If you have discovered a security vulnerability, please do **not** open a public
issue. Follow the [security reporting process](./SECURITY.md) instead.

## Contributing Code

### Sign Your Commits

All commits must include a `Signed-off-by` line using your real name and email
address:

```
Signed-off-by: Your Name <you@example.com>
```

Use `git commit --signoff` to add this automatically.

### Pull Request Process

1. Open an issue describing your proposed change
2. Fork the repository and create a branch from `main`
3. Make your changes, including tests and documentation updates
4. Ensure `make test` passes
5. Submit a pull request referencing your issue

### Code Style

* Go code follows standard `gofmt` formatting
* Keep commits focused — one logical change per commit

## Code of Conduct

Follow the [Golden Rule](https://en.wikipedia.org/wiki/Golden_Rule). If you'd
like more specific guidelines, see the [Contributor Covenant Code of Conduct](./CODE_OF_CONDUCT.md).
