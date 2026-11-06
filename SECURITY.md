# Security Policy

## Scope

CDIN Registry accepts Product Definitions, catalog indexes and component release metadata, and will eventually retrieve immutable source revisions. Security reports may include parser vulnerabilities, path traversal, unsafe Git invocation, credential exposure, digest bypass, denial of service, dependency-resolution confusion, unbounded resource consumption and partial workspace publication.

See [Security and trust boundaries](docs/security/trust-boundaries.md) for the explicit Registry trust model, and [Resource budgets](docs/implementation/resource-budgets.md) for enforced input, concurrency, resolver and cache limits. Registry does not provide publisher signing/attestation, SBOM or component vulnerability scanning, a build sandbox, or deployment firewall enforcement.

## Reporting a Vulnerability

Please use GitHub's private vulnerability reporting for this repository:

<https://github.com/cdin-editor/registry/security/advisories/new>

Do not post security-sensitive details in public issues or pull requests. Include the affected version or commit, operating system, reproduction steps, expected impact and a minimal proof of concept when safe. Remove credentials, private repository URLs and personal data from logs.

## Response and Disclosure

Maintainers will coordinate triage, repair and disclosure with the reporter. No response-time SLA is promised.
