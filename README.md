# Docs

Lorem ipsum dolor sit amet

## Note Command

### Generate Swagger

```bash
swag init -g cmd/api/main.go -o docs
go run cmd/api/main.go
swag init -g cmd/main.go -o docs --parseInternal
```
