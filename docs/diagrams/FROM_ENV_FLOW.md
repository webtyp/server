# FromEnv flow

`httpd.FromEnv()` reads the doors and the certificate of a self-hosted server from the environment that its Docker
Compose sets. Every branch below has a test in `tests/fromenv_test.go`.

```mermaid
flowchart TD
    A[FromEnv] --> B{APP_DOMAIN, CLOUDFLARE_DNS_API_TOKEN (or _FILE)<br/>and INTERNET_PORT all empty?}
    B -->|yes| C[Zero Config<br/>local certificate authority, one door]
    B -->|no| D{APP_DOMAIN empty?}
    D -->|yes| E{INTERNET_PORT set?}
    E -->|yes| F[ErrEnvInternetWithoutDomain]
    E -->|no| G[ErrEnvTokenWithoutDomain]
    D -->|no| H{CLOUDFLARE_DNS_API_TOKEN (or _FILE) empty?}
    H -->|yes| I[ErrEnvDomainWithoutToken]
    H -->|no| J[Build the Cloudflare DNS-01 provider]
    J --> K{Provider built?}
    K -->|no| L[Return the provider error]
    K -->|yes| M[Config with TLS DNS01 + Domain<br/>and InternetPort when set]
```
