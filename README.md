# MongoDB Dumper

[![Go Report Card](https://goreportcard.com/badge/github.com/yourusername/mongodb-dumper)](https://goreportcard.com/report/github.com/0oz/mongodb-dumper)
[![License](https://img.shields.io/github/license/yourusername/mongodb-dumper)](https://github.com/0oz/mongodb-dumper/blob/main/LICENSE)
[![Go Version](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://golang.org/doc/devel/release.html#go1.24)

A robust Go application that automates MongoDB database backups to Backblaze B2 Cloud Storage (via S3 API) on a scheduled basis. Designed for reliability, flexibility, and seamless integration with modern cloud-native environments.

## 🚀 Features

- **Automated Backups**: Schedule backups hourly or at custom intervals
- **Multi-Environment Support**: Separate configurations for staging and production
- **Cloud Storage Integration**: Direct upload to Backblaze B2 via S3-compatible API
- **Kubernetes Integration**: Ready-to-use deployment manifests for cloud environments
- **Native BSON Format**: Backups stored in standard MongoDB format for easy restoration
- **Flexible Configuration**: Configure via environment variables, command-line flags, or config files
- **Retention Policies**: Configurable backup retention and cleanup strategies
- **Monitoring & Alerts**: Prometheus metrics and failure notifications (optional)

## 📋 Requirements

- **Go 1.24+** (for building from source)
- **MongoDB Tools** (`mongodump` in PATH)
- **Backblaze B2 Account** with S3-compatible API enabled
- **Kubernetes Cluster** (for production deployment)

## ⚙️ Configuration

The application can be configured using environment variables, command-line flags, or by providing a configuration file:

| Environment Variable | Flag             | Description                                     | Required | Default                 |
|----------------------|------------------|-------------------------------------------------|----------|-------------------------|
| MONGO_URI            | --mongo-uri      | MongoDB connection string URI                   | Yes      | -                       |
| MONGO_DATABASE       | --database       | MongoDB database to backup (empty = all DBs)    | No       | (all databases)         |
| ENVIRONMENT          | --env            | Environment (staging or production)             | Yes      | -                       |
| S3_ENDPOINT          | --s3-endpoint    | S3 endpoint URL for Backblaze                   | Yes      | -                       |
| S3_REGION            | --s3-region      | S3 region                                       | Yes      | -                       |
| S3_BUCKET            | --s3-bucket      | S3 bucket name                                  | Yes      | -                       |
| S3_ACCESS_KEY        | --s3-access-key  | S3 access key                                   | Yes      | -                       |
| S3_SECRET_KEY        | --s3-secret-key  | S3 secret key                                   | Yes      | -                       |
| TEMP_DIR             | --temp-dir       | Temporary directory for backups                 | No       | /tmp                    |
| BACKUP_INTERVAL      | --interval       | Backup interval (1h, 6h, 24h)                   | No       | (one-time run)          |
| ONE_TIME             | --one-time       | Run a single backup and exit                    | No       | false                   |
| LOG_FORMAT           | --log-format     | Log format: json, console, pretty, compact      | No       | pretty                  |
| LOG_LEVEL            | --log-level      | Logging level: debug, info, warn, error         | No       | info                    |
| RETENTION_DAYS       | --retention-days | Number of days to keep backups                  | No       | 30                      |
| METRICS_ENABLED      | --metrics        | Enable Prometheus metrics endpoint              | No       | false                   |
| METRICS_PORT         | --metrics-port   | Port for Prometheus metrics                     | No       | 9090                    |
| -                    | --env-file       | Path to .env file for environment variables     | No       | .env                    |

## 🏃 Running Locally

### From Source

```bash
# Clone the repository
git clone https://github.com/yourusername/mongodb-dumper.git
cd mongodb-dumper

# Build the application
go build -o dumper ./cmd/dumper

# Run with configuration
./dumper \
  --mongo-uri="mongodb://username:password@hostname:port" \
  --env=staging \
  --s3-endpoint="https://s3.backblazeb2.com" \
  --s3-region="us-west-001" \
  --s3-bucket="your-backup-bucket" \
  --s3-access-key="your-access-key" \
  --s3-secret-key="your-secret-key" \
  --log-format=pretty \
  --interval=1h
```

### Using Configuration File

You can also create a `.env` file:

```
MONGO_URI=mongodb://username:password@hostname:port
ENVIRONMENT=staging
S3_ENDPOINT=https://s3.backblazeb2.com
S3_REGION=us-west-001
S3_BUCKET=your-backup-bucket
S3_ACCESS_KEY=your-access-key
S3_SECRET_KEY=your-secret-key
LOG_FORMAT=pretty
BACKUP_INTERVAL=1h
```

Then run:

```bash
./dumper --env-file=.env
```

## 🐳 Docker

Build and run using Docker:

```bash
# Build the Docker image
docker build -t mongodb-dumper:latest .

# Run the Docker container
docker run -d \
  -e MONGO_URI="mongodb://username:password@hostname:port" \
  -e ENVIRONMENT="staging" \
  -e S3_ENDPOINT="https://s3.backblazeb2.com" \
  -e S3_REGION="us-west-001" \
  -e S3_BUCKET="your-backup-bucket" \
  -e S3_ACCESS_KEY="your-access-key" \
  -e S3_SECRET_KEY="your-secret-key" \
  -e LOG_FORMAT="pretty" \
  -e BACKUP_INTERVAL="1h" \
  mongodb-dumper:latest
```

### Using Docker Compose

```yaml
# docker-compose.yml
version: '3'
services:
  mongodb-dumper:
    build: .
    restart: unless-stopped
    environment:
      - MONGO_URI=mongodb://username:password@mongodb:27017/database?authSource=admin
      - ENVIRONMENT=staging
      - S3_ENDPOINT=https://s3.backblazeb2.com
      - S3_REGION=us-west-001
      - S3_BUCKET=your-backup-bucket
      - S3_ACCESS_KEY=your-access-key
      - S3_SECRET_KEY=your-secret-key
      - BACKUP_INTERVAL=1h
```

Run with:

```bash
docker-compose up -d
```

## ☸️ Kubernetes Deployment

This application can be deployed to Kubernetes using the provided manifests in the `k8s` directory.

```bash
# Create the required secrets
kubectl create secret generic mongodb-dumper-secrets \
  --from-literal=MONGO_URI="mongodb://username:password@hostname:port" \
  --from-literal=S3_ACCESS_KEY="your-access-key" \
  --from-literal=S3_SECRET_KEY="your-secret-key"

# Deploy the application
kubectl apply -f k8s/mongodb-dumper.yaml
```

The provided Kubernetes manifest includes:

- A CronJob to run backups on schedule
- A ConfigMap for non-sensitive configuration
- A Secret for sensitive credentials
- Resource limits and requests

## 📦 Backup Details

### Backup Format

The backups are stored in MongoDB's archive format (BSON), which can be easily restored using the `mongorestore` command.

### Backup Naming Convention

Backups are stored with the following naming convention:

```
{environment}/{date}/{database}-{environment}-{timestamp}.archive
```

For example:

- `staging/2023-04-15/my-database-staging-2023-04-15T12-00-00Z.archive`
- `production/2023-04-15/my-database-production-2023-04-15T12-00-00Z.archive`

### Backup Restoration

To restore a database from backup:

```bash
# Download the backup from Backblaze B2
# Then restore using mongorestore:
mongorestore --uri="mongodb://username:password@hostname:port" --archive=/path/to/downloaded/backup.archive

# Restore a specific collection
mongorestore --uri="mongodb://username:password@hostname:port/database?authSource=admin" \
  --nsInclude="database.collection" /path/to/collection.bson
```

## 🧪 Testing

Run the test suite:

```bash
go test -v ./...
```

Run integration tests (requires local MongoDB instance):

```bash
go test -v -tags=integration ./...
```

## 📊 Monitoring

When metrics are enabled, MongoDB Dumper exposes Prometheus metrics at `/metrics` on the configured port (default: 9090).

Key metrics available:

- `mongodb_dumper_backup_total` - Total number of backups attempted
- `mongodb_dumper_backup_success_total` - Successful backups
- `mongodb_dumper_backup_failure_total` - Failed backups
- `mongodb_dumper_backup_duration_seconds` - Backup duration in seconds
- `mongodb_dumper_upload_size_bytes` - Size of uploaded backup archives

## 🔒 Security Considerations

- Always use MongoDB connection strings with minimal required permissions
- Store sensitive credentials as Kubernetes secrets or environment variables
- Consider using service accounts with restricted permissions for S3 access
- Encrypt backups at rest by enabling server-side encryption in your S3 bucket

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add some amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## 📜 License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## 📞 Support

For support, please open an issue on the GitHub repository or contact the maintainers directly.
