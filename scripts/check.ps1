$ErrorActionPreference = 'Stop'
Push-Location (Join-Path $PSScriptRoot '..')
try {
    $files = & go list -f '{{range .GoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .TestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}{{range .XTestGoFiles}}{{$.Dir}}/{{.}}{{"\n"}}{{end}}' ./...
    if ($LASTEXITCODE -ne 0) { throw 'go list failed' }
    $unformatted = & gofmt -l ($files | Where-Object { $_ })
    if ($unformatted) { throw "Run gofmt on: $unformatted" }
    & go test ./... -count=1
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
    & go vet ./...
    if ($LASTEXITCODE -ne 0) { throw 'go vet failed' }
    & go build ./...
    if ($LASTEXITCODE -ne 0) { throw 'go build failed' }
} finally { Pop-Location }
