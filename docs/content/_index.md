---
title: forgemirror
layout: hextra-home
---

{{< hextra/hero-badge link="docs/getting-started/" >}}
  <span>Get started</span>
  {{< icon name="arrow-circle-right" attributes="height=14" >}}
{{< /hextra/hero-badge >}}

<div class="hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
  Your Forgejo stays private.&nbsp;<br class="hx:sm:block hx:hidden" />Your code goes public, instantly.
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-12">
{{< hextra/hero-subtitle >}}
  forgemirror listens for Forgejo push webhooks and mirrors the repository&nbsp;<br class="hx:sm:block hx:hidden" />to GitHub, Codeberg, GitLab or any other Git forge within seconds.
{{< /hextra/hero-subtitle >}}
</div>

<div class="hx:mb-6">
{{< hextra/hero-button text="Read the docs" link="docs/" >}}
</div>

<div class="hx:mt-6"></div>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Push-driven"
    subtitle="No polling and no cron. Every push triggers one clone and a parallel push to all targets, and pushes that arrive during a run are coalesced."
    icon="lightning-bolt"
  >}}
  {{< hextra/feature-card
    title="Works behind a firewall"
    subtitle="Forgejo only has to reach forgemirror. Nothing on the internet needs access to your instance."
    icon="shield-check"
  >}}
  {{< hextra/feature-card
    title="One target, many repositories"
    subtitle="Target URLs are templates: one entry with `{{ .Name }}` in its path covers every repository."
    icon="template"
  >}}
  {{< hextra/feature-card
    title="Filters"
    subtitle="Include and exclude repositories by pattern, restrict branches, skip private repositories and forks, or opt in per repository with a topic."
    icon="filter"
  >}}
  {{< hextra/feature-card
    title="Secrets stay secret"
    subtitle="Tokens never touch disk, argv or logs. Credentials can come from mounted files, as Kubernetes expects."
    icon="key"
  >}}
  {{< hextra/feature-card
    title="Ready to operate"
    subtitle="Prometheus metrics, structured logs, health probes, graceful shutdown, a Helm chart and signed multi-arch images."
    icon="chart-bar"
  >}}
{{< /hextra/feature-grid >}}
