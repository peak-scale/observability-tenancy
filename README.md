# Capsule ❤️ Cortex

> [!IMPORTANT]
> This project is a permanent hard-fork of the [origin project](https://github.com/blind-oracle/cortex-tenant).

![Capsule Cortex](docs/images/logo.png)

<p align="center">
<a href="https://github.com/projectcapsule/cortex-proxy/releases/latest">
  <img alt="GitHub release (latest SemVer)" src="https://img.shields.io/github/v/release/projectcapsule/cortex-proxy?sort=semver">
</a>
<a href="https://app.fossa.com/projects/git%2Bgithub.com%2Fprojectcapsule%2Fcortex-proxy?ref=badge_small" alt="FOSSA Status"><img src="https://app.fossa.com/api/projects/git%2Bgithub.com%2Fprojectcapsule%2Fcortex-proxy.svg?type=small"/></a>
<a href="https://artifacthub.io/packages/search?repo=cortex-proxy">
  <img src="https://img.shields.io/endpoint?url=https://artifacthub.io/badge/repository/cortex-proxy" alt="Artifact Hub">
</a>
<a href="https://codecov.io/gh/projectcapsule/cortex-proxy" >
 <img src="https://codecov.io/gh/projectcapsule/cortex-proxy/graph/badge.svg?token=HER3gBKdqU"/>
 </a>
</p>

Prometheus remote write proxy which marks timeseries with a Cortex/Mimir tenant ID based on labels.

## Overview

![Architecture](docs/images/capsule-cortex.gif)

Cortex/Mimir tenants (separate namespaces where metrics are stored to and queried from) are identified by `X-Scope-OrgID` HTTP header on both writes and queries.

This software solves the problem using the following logic:

- Receive Prometheus remote write
- Search each timeseries for a specific label name and extract a tenant ID from its value.
  If the label wasn't found then it can fall back to a configurable default ID.
  If none is configured then the write request will be rejected with HTTP code 400
- Optionally removes this label from the timeseries
- Groups timeseries by tenant
- Issues a number of parallel per-tenant HTTP requests to Cortex/Mimir with the relevant tenant HTTP header (`X-Scope-OrgID` by default)

## Documentation

See the [Documentation](docs/README.md) for more information on how to use this addon.

## Support

This addon is developed by the community. For enterprise support (production ready setup,tailor-made features) reach out to [Capsule Supporters](https://projectcapsule.dev/support/)

## License

[![FOSSA Status](https://app.fossa.com/api/projects/git%2Bgithub.com%2Fprojectcapsule%2Fcortex-proxy.svg?type=large)](https://app.fossa.com/projects/git%2Bgithub.com%2Fprojectcapsule%2Fcortex-proxy?ref=badge_large)
