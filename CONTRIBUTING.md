# Contributing

Thank you for helping. This is a security-sensitive tool, so a few rules matter.

## Set up

You need Go (the version in `go.mod`), `git` and `kubectl`. For the full check you also need Docker, k3d and Helm.
Run every command inside the repository folder.

- `make ci` runs the checks that need no cluster. Run it before every pull request.
- `make helm-k3d` installs the Helm chart on a throwaway local cluster and runs an investigation. Run it when you change Go code or the chart.

## Rules

1. **Tests must be able to fail.** Break the code on purpose and see the test fail, then restore it.
2. **No real secrets, and no secret-shaped text, in the repository.** GitHub refuses pushes that look like they contain keys. In tests, write fake tokens in pieces, for example `"sk" + "_live_..."`.
3. **Security changes come with a test.** This covers the tool list, the policy, masking, and the handling of untrusted log text.
4. **Do not widen what the tools can do.** Tools stay read-only. A new tool needs a reason, a parameter check and a test that runs it the way a model's request runs it.
5. **Docs stay plain.** Short statements, simple words, readable for people who do not speak English as a first language. Do not put numbers in the docs that a test run can change. Point to `accuracy/RESULTS.md` instead.
6. **Accuracy claims need a run.** Do not edit `accuracy/RESULTS.md` by hand. See `accuracy/README.md`.

## License

By contributing you agree that your contribution is licensed under the Apache License 2.0 (see `LICENSE`, section 5).
