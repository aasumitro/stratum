# Security Policy

> **Template placeholder.** Stratum is a template: replace `security@your-domain.example` below with an
> address your team actually watches before you ship a product built on it, and update the supported
> versions table to match your releases.

## Reporting a vulnerability

Please report suspected vulnerabilities privately by email to **security@your-domain.example**.
Do not open a public issue, pull request or discussion for a security problem.

Include as much of the following as you can:

- the affected component (API, worker, web app, Studio) and version or commit;
- the steps to reproduce, or a proof of concept;
- the impact you expect (for example data exposure, privilege escalation, denial of service);
- any logs, request IDs or screenshots that help, with secrets and personal data removed.

## What to expect

- An acknowledgement within **3 business days**.
- An initial assessment, including whether we consider it a vulnerability and its severity, within
  **10 business days**.
- Updates as the fix progresses, and a coordinated disclosure date agreed with you. We credit
  reporters in the release notes unless you ask us not to.

Please give us a reasonable time to fix the issue before disclosing it, and do not access, modify or
delete data that is not yours while testing.

## Supported versions

Security fixes go into the latest minor release.

| Version | Supported |
|---|---|
| 0.7.x | Yes |
| < 0.7 | No |

## Scope

In scope: this repository's API, worker, web app (`ui/app`) and Studio (`ui/studio`), and their
default configuration. Out of scope: third-party services the template integrates with (Supabase,
Stripe, Xendit, your hosting provider), which have their own reporting channels, and findings that
need physical access or a compromised operator machine.
