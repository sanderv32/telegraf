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
#### System Metrics
- POOL_PERFORMANCE:
    - pool
    - disktype
    - array

- NETWORK:
    - controller
    - interface
    - array

- CPU:
    - controller
    - array

- CACHE_HITS:
    - controller
    - array
    
#### Data Metrics:
All possible metrics are written in de [Intelliflash API Guide](https://github.com/Tegile/IntelliFlash-API-Reference-Guides). 
- array


#### Capacity Metrics:
- array
- pool

### Example output:
