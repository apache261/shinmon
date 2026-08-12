# TLS files

This directory is only a mount point. Do not commit certificates or private keys.

The HTTPS HAProxy profile expects `edge.pem`, containing the leaf certificate,
any intermediate certificates, and its private key. The dashboard TLS profile
uses separate `dashboard.crt` and `dashboard.key` files. Production deployments
should mount these from their secrets manager through `GATEWAY_TLS_DIR` and
`DASHBOARD_TLS_DIR` rather than storing them in the repository.
