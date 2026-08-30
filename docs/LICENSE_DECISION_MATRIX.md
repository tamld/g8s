# License Decision Matrix

> **Governing SSoT**: `manifest.json` (`license: "MIT OR Apache-2.0"`)  
> **Status**: Active (Stage 1 Dual-Licensing)  
> **Related Issues**: #181 (DEBT-53)

---

## Overview

`g8s` is dual-licensed under the **MIT License** and the **Apache License 2.0**. Users, developers, and enterprise adopters may choose either license at their option to best fit their technical, operational, and legal requirements.

This document serves as a decision framework for selecting the appropriate license when consuming, integrating, or contributing to `g8s`.

---

## Decision Matrix Table

| Scenario | Recommended license | Reason |
| --- | --- | --- |
| Personal project | MIT | No friction |
| Open source product | MIT | Ecosystem |
| Enterprise integration | Apache 2.0 | Patent grant |
| SaaS offering | AGPL v3 (future) | "Viral" copyleft |
| Trade secret | BSL 1.1 (future) | Delayed open |

---

## License Option Breakdown

### 1. MIT License (`LICENSE-MIT`)
* **Target Audience**: Personal, hobbyist, educational, and general open-source projects.
* **Characteristics**:
  * Highly permissive with minimal restrictions.
  * Simple attribution requirement (retain copyright and permission notice).
  * Maximum compatibility across standard open-source package registries and toolchains.
* **When to choose**: When you need zero-friction integration without additional legal or compliance overhead.

### 2. Apache License 2.0 (`LICENSE-APACHE-2.0`)
* **Target Audience**: Enterprise software, corporate deployments, and commercial vendor integrations.
* **Characteristics**:
  * Permissive terms with formal terms for copyright and patent grants.
  * Explicit patent license grant (§3) and defensive patent retaliation termination clause protecting users and contributors against patent trolls.
  * Standard choice for foundational cloud and systems infrastructure (e.g., Kubernetes, TensorFlow, Swift).
* **When to choose**: When corporate legal policy mandates explicit patent protections and clear contribution IP boundaries.

---

## Future Roadmap Licensing Stages

The project follows a phased multi-stage licensing evolution strategy:

### Stage 1 (Current / Active): Dual License (MIT + Apache 2.0)
* Retains MIT for friction-free community adoption.
* Adds Apache 2.0 for enterprise patent assurance.

### Stage 2 (Future / Roadmap): BSL 1.1 ("Business Source License")
* *Trigger*: Emerging commercial enterprise on-prem demand.
* *Model*: Free for non-production/development use, commercial license required for production deployments beyond defined thresholds; converts automatically to Apache 2.0 after a fixed term (e.g., 4 years).
* *Reference Precedents*: Sentry, CockroachDB, MariaDB MaxScale.

### Stage 3 (Future / Roadmap): AGPL v3 + Commercial Dual-Licensing
* *Trigger*: Managed cloud SaaS reselling demand.
* *Model*: Strong network-copyleft protection for hosted services to prevent closed-source cloud monetization without upstream contribution.
* *Reference Precedents*: GitLab, Mattermost, MinIO.

---

## Contributor Information

Contributors must agree to the Contributor License Agreement (CLA) ensuring contributions are dual-licensable under both MIT and Apache 2.0 licenses (see `CLA.md`, to be added in a separate DEBT).
