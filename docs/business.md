# BharatCode — Business & Go-To-Market

> This is a strategy document, not a product spec. It states the wedge, the
> business model, and the path from open-source project to a fundable company.
> It is deliberately honest about what is and isn't a moat.

## The one-line thesis

**BharatCode is the compliant, sovereign AI coding platform for regulated
enterprises — India first.** The open-source CLI is the wedge and the
distribution engine; the paid, self-hostable enterprise control plane is the
business.

## Why this is a business and not just a feature

"OpenCode but data stays in India" is, on its own, a deployment config — anyone
could fork it. That is the correct objection, and the answer is: **the CLI is
not the product we sell.** The CLI earns trust and distribution. The revenue is
the layer a bank's security and platform teams pay for:

- **Self-hosted / on-prem control plane** — runs inside the customer's VPC or
  data center; no code, prompts, or telemetry leave their boundary.
- **Governance** — SSO/SAML, RBAC, per-team policy (which models, which tools,
  which repos), audit logs of every agent action and tool call.
- **Spend governance** — org-wide INR budgets, per-team cost attribution, model
  routing (cheap open-weight by default, frontier only on approval).
- **Model sovereignty** — bring-your-own endpoint: on-prem GPUs, India-hosted
  inference (Sarvam, Krutrim), or local models. The control plane brokers them.
- **Compliance posture** — DPDP-aligned data handling, deployment attestations,
  and the certifications a regulated buyer's procurement requires.

The open CLI is MIT and forkable. The moat is **compliance certifications,
enterprise relationships, and being the trusted Indian vendor** — not the code.

## The wedge: regulated Indian enterprises (BFSI first)

The incumbents are *structurally* locked out of this segment:

- **Claude Code** is Anthropic-only. **Codex CLI** is OpenAI-only. Neither can
  tell an Indian bank "your source code never leaves the country" — that would
  break their business model.
- India's **DPDP Act** (data protection, now in force) and sector regulators
  (RBI for BFSI, etc.) push regulated industries toward data localization and
  vendor controls. "Send our proprietary code to a US cloud" is increasingly a
  compliance blocker, not a preference.

That is a real wedge with a regulatory tailwind. BFSI is the beachhead: it has
budget, hard compliance rules, and large developer populations. Govt/PSU,
healthcare, and defense-adjacent follow the same logic.

## The funnel: OSS → enterprise

1. **Open-source CLI (free, MIT)** — distribution + credibility. Developers
   adopt it because it's genuinely good, open-weight-friendly, and cheap to run.
2. **Individual / team tier** — hosted convenience, shared config, prompt
   libraries; low-friction paid upgrade for small teams.
3. **Enterprise (self-hosted)** — the control plane above. Sold to the platform
   and security org, not the individual developer. This is the revenue.

The CLI's job is to be in developers' hands so that when their employer asks
"how do we let engineers use AI coding agents without a compliance incident,"
BharatCode is already the trusted answer.

## Differentiation (honest)

| Axis | BharatCode | Claude Code | OpenCode | Codex CLI |
|---|---|---|---|---|
| License | MIT | Closed | MIT | Apache-2.0 |
| Open-weight-first | Yes | No | Multi-provider | No (OpenAI) |
| Data residency / local-first | **Core** | No | Possible, not a focus | No |
| Cost discipline (INR) | **Built-in ledger** | No | No | No |
| Regulated-enterprise control plane | **Roadmap (the business)** | N/A | No | No |
| OS-level sandbox | **Yes** | Partial | No | Yes |

We do **not** claim to beat frontier models on raw agentic coding — open weights
trail there today. Our buyer is the cost- and compliance-constrained one, which
is a narrower but defensible and underserved market.

## What we are NOT betting on

- **Not** "our agent loop is smarter." Agentic loops are table stakes; bigger
  teams with better models win that race.
- **Not** a global consumer-developer land-grab against OpenCode. We win a
  *niche they can't enter*, then expand.

## Expansion path

India (BFSI → govt/PSU → healthcare) → the broader **Global South** where the
same data-sovereignty + cost problem exists (Indonesia, Brazil, Nigeria, the
Gulf). Same product, same thesis, different flag.

## What turns "project" into "fundable" — the next 90 days

These are validation moves, not features:

1. **3 design partners** in BFSI/govt who say, on record, "we cannot use
   Claude Code / Codex for compliance reasons." One CISO sentence > any feature.
2. **1 reference deployment** — a real team using BharatCode daily on a real
   codebase, self-hosted.
3. **Reframe externally** from "OpenCode for India" (caps the ceiling, reads as
   clone) to **"sovereign AI coding platform for regulated enterprises."**
4. **10 real OSS users**, then instrument what they ask for.

## Status

Early. The product is a credible prototype of the wedge: a working, open,
open-weight-first CLI with the sovereignty and cost primitives already in place.
The business is the next 12–18 months of landing real regulated customers — not
the next 18 features.
