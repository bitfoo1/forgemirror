---
title: ForgeMirror
layout: hextra-home
---

<div class="fm-glow" aria-hidden="true"></div>

<div class="fm-badge">
{{< hextra/hero-badge link="docs/getting-started/" >}}
  <span>Get started</span>
  {{< icon name="arrow-circle-right" attributes="height=14" >}}
{{< /hextra/hero-badge >}}
</div>

<div class="fm-headline hx:mt-6 hx:mb-6">
{{< hextra/hero-headline >}}
  Your Forgejo stays private.&nbsp;<br class="hx:sm:block hx:hidden" />Your code goes public, instantly.
{{< /hextra/hero-headline >}}
</div>

<div class="hx:mb-12">
{{< hextra/hero-subtitle >}}
  ForgeMirror listens for Forgejo push webhooks and mirrors the repository&nbsp;<br class="hx:sm:block hx:hidden" />to GitHub, Codeberg, GitLab or any other Git forge within seconds.
{{< /hextra/hero-subtitle >}}
</div>

<div class="fm-button hx:mb-6">
{{< hextra/hero-button text="Read the docs" link="docs/" >}}
</div>

{{< screenshot name="web-ui" class="fm-ribbon" alt="The ForgeMirror web UI: overview tiles with the configured target types, and a mirrored repository with the last result of every sync step on its Forgejo target." >}}

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Push-driven"
    class="fm-card"
    subtitle="Every push triggers one clone and a parallel push to all targets; pushes that arrive during a run are coalesced. A schedule can sweep every repository too."
    icon="lightning-bolt"
  >}}
  {{< hextra/feature-card
    title="Review before publishing"
    class="fm-card"
    subtitle="Every run plans first. The web UI shows the diff per target, and in review mode nothing is pushed until you press Sync."
    icon="eye"
  >}}
  {{< hextra/feature-card
    title="Creates and syncs repositories"
    class="fm-card"
    subtitle="On GitHub, GitLab and Forgejo targets: create missing repositories and keep settings and labels in line. A private repository never becomes public."
    icon="collection"
  >}}
  {{< hextra/feature-card
    title="Releases and container images"
    class="fm-card"
    subtitle="Copies releases with their attachments, and the container images of public repositories with signatures intact."
    icon="cube"
  >}}
  {{< hextra/feature-card
    title="Works behind a firewall"
    class="fm-card"
    subtitle="Forgejo only has to reach ForgeMirror. Nothing on the internet needs access to your instance."
    icon="shield-check"
  >}}
  {{< hextra/feature-card
    title="One target, many repositories"
    class="fm-card"
    subtitle="Target URLs are templates: one entry with `{{ .Name }}` in its path covers every repository."
    icon="template"
  >}}
  {{< hextra/feature-card
    title="Filters"
    class="fm-card"
    subtitle="Include and exclude repositories by pattern, restrict branches, skip private repositories and forks, or opt in per repository with a topic."
    icon="filter"
  >}}
  {{< hextra/feature-card
    title="Secrets stay secret"
    class="fm-card"
    subtitle="Tokens never touch disk, argv or logs. Credentials can come from mounted files, as Kubernetes expects."
    icon="key"
  >}}
  {{< hextra/feature-card
    title="Ready to operate"
    class="fm-card"
    subtitle="Prometheus metrics, structured logs, health probes, graceful shutdown, a Helm chart and signed multi-arch images."
    icon="chart-bar"
  >}}
{{< /hextra/feature-grid >}}
