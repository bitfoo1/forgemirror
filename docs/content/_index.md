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

{{< screenshot name="web-ui" class="fm-ribbon" priority="true" alt="The ForgeMirror web UI in review mode: the pipeline from its triggers through ForgeMirror to the GitHub, Codeberg and GitLab targets, with Codeberg on hold, and two repositories whose current diffs show the pending ref changes per target." >}}

<h2 class="fm-sr-only">Features</h2>

{{< hextra/feature-grid >}}
  {{< hextra/feature-card
    title="Push-driven"
    class="fm-card"
    subtitle="Pushes and schedules compute current state, clone once when needed and publish to permitted targets in parallel. Follow-up events are coalesced."
    icon="lightning-bolt"
  >}}
  {{< hextra/feature-card
    title="Review before publishing"
    class="fm-card"
    subtitle="Every run plans first. Review mode needs explicit publication authorization, and optional first-publication grants bind a source repository to its destination."
    icon="eye"
  >}}
  {{< hextra/feature-card
    title="Creates and syncs repositories"
    class="fm-card"
    subtitle="Create repositories and synchronize metadata, labels and public mirror identity, including an explicit contribution link. Private sources stay private."
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
    subtitle="Filter repositories, restrict which branch pushes trigger work, skip private repositories and forks, or opt in with a topic. Mirrors still copy all branches and tags."
    icon="filter"
  >}}
  {{< hextra/feature-card
    title="Secrets stay secret"
    class="fm-card"
    subtitle="Tokens never touch disk, argv or logs. Credentials can come from mounted files, as Kubernetes expects."
    icon="key"
  >}}
  {{< hextra/feature-card
    title="Complete Git LFS mirrors"
    class="fm-card"
    subtitle="Automatically transfer source-history LFS objects before Git refs. Repair missing objects even when refs already match."
    icon="cube"
  >}}
  {{< hextra/feature-card
    title="Pause and recover"
    class="fm-card"
    subtitle="Pause a destination or repository, resume fresh state, and let bounded durable retries recover transient failures without retrying successful targets."
    icon="adjustments"
  >}}
  {{< hextra/feature-card
    title="Explain every hold"
    class="fm-card"
    subtitle="Retained history, per-target reasons and next actions distinguish current blockers from old failures. Optional webhook notifications report blocks and recovery."
    icon="eye"
  >}}
  {{< hextra/feature-card
    title="Ready to operate"
    class="fm-card"
    subtitle="Prometheus metrics, structured logs, health probes, graceful shutdown, a Helm chart and signed multi-arch images."
    icon="chart-bar"
  >}}
{{< /hextra/feature-grid >}}
