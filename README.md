# forgemirror

Webhook-driven service that mirrors Forgejo repositories to GitHub, Codeberg, or any Git forge — instantly on push.

- Documentation: `docs/` (Hugo site; `mise run docs:serve` for a local preview)
- Configuration reference: [`forgemirror.example.yaml`](forgemirror.example.yaml)
- Kubernetes: [`deploy/`](deploy/README.md) (kustomize) or the [Helm chart](charts/forgemirror/README.md)
