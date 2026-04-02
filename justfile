default:
    @just --list

build:
    go build -o wpswag .

release:
    npx semantic-release

ci: build

ci-release: ci release
