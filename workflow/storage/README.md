## Backend comparisons

| Feature | SQLite | CosmosDB | Azure Blob (this) |
|---------|--------|----------|-------------------|
| Storage Type | Local file | Cloud NoSQL | Cloud Object Store |
| Transactions | Native | Batch | Manual cleanup |
| Search | SQL queries | Cosmos queries | Tag-based + listing |
| Cost | Free | High | Low-Medium |
| Scale | Single node | Distributed | Distributed |
| Best For | Development or Production(if distributed file system) | Production (speed) | Production (cost-effective) |
