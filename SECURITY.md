# Security policy

## Report a problem

Please report security problems **privately**. Use the **Security** tab of this repository and choose **Report a vulnerability**.
Do not open a public issue with exploit details, and do not paste real secrets anywhere.

This is a personal open-source project. There is no guaranteed response time, but every report is read.

## What counts as a security problem

- A secret or personal data that reaches the model service, a report or a log although it should have been masked.
- A way to make the tools do more than read: another namespace, a write action, a command the fixed tool list does not allow.
- Text in a log or a Kubernetes object that makes the AI ignore its rules and gets past the policy checks.
- A way to read credentials, such as the model key, through the tools or the reports.

## Supported versions

Only the latest `main` is supported.

## How the project protects itself

The layers, what each one stops, and what is still not covered are described in [docs/SECURITY.md](docs/SECURITY.md).
Where data goes is described in [docs/DATA-FLOW.md](docs/DATA-FLOW.md).
