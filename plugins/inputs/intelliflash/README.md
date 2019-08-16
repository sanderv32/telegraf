# Intelliflash statistics plugin
The Intelliflash plugin gathers statistics from the Intelliflash REST API

### Configuration:
```toml
[[inputs.intelliflash]]
    servers = ["localhost", "127.0.0.1"]

    username = "admin"
    password = "admin"
```

### Tags:

- POOL_PERFORMANCE:
    - pool
    - disktype

- NETWORK:
    - controller
    - interface

- CPU:
    - controller

- CACHE_HITS:
    - controller

### Data Metrics:
All possible metrics are written in de [Intelliflash API Guide](https://github.com/Tegile/IntelliFlash-API-Reference-Guides).

### Example output:
