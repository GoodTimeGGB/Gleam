# Security Policy

## Supported versions

| Version | Supported |
| --- | --- |
| Latest release on the default branch | Yes |
| Older tagged releases | Best effort; critical fixes may be backported at maintainer discretion |

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Preferred channels (in order):

1. **GitHub Security Advisory (private)** — on the affected repository: **Security → Advisories → Report a vulnerability**  
   Example for Gleam: https://github.com/gleam-ai/Gleam/security/advisories/new
2. **Email:** markgus0@proton.me with subject `[SECURITY] <short title>`

Include:

- Affected repository and version / commit
- Impact and attack scenario
- Reproduction steps or proof of concept (kept private)
- Whether you are available for coordinated disclosure

## SLA

| Step | Target |
| --- | --- |
| Initial acknowledgement | within **3 business days** |
| Triage (severity / affected versions) | within **7 business days** |
| Fix or mitigation plan shared | within **30 days** for High/Critical when feasible |
| Public disclosure | coordinated with reporter after a fix or mitigating release is available |

If we cannot meet a target, we will say so and give a revised estimate.

## Scope notes (Gleam)

Gleam is a **local-first** desktop agent. Reports involving:

- Unexpected network egress / data leaving the machine without user intent
- Privilege escalation via tools / shell / MCP connectors
- Secrets written to disk or logs in plaintext beyond documented behavior
- Supply-chain issues in release binaries or GitHub Actions

…are especially valuable.

## Safe harbor

We will not pursue legal action against good-faith research that:

- Avoids privacy violations, destruction of data, and service disruption
- Keeps vulnerability details private until coordinated disclosure
- Does not exploit the issue beyond what is needed to demonstrate impact

## Acknowledgments

With reporter consent, we credit security researchers in release notes or the advisory.
