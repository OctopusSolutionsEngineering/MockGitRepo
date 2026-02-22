A mock Git server that create disposable Git repos for testing.

## Usage

Start the server:

```bash
podman run --name mockgitserver -p 8080:8080 --name git-server mockgitserver
```

Clone the repository:

```bash
cd /tmp
git clone @http://myusername@localhost:8080/platformhubrepo
```

## Adding new sample repos

1. Add a directory in `repotemplate`
2. Run `git init` in the new directory
3. Run `git config http.receivepack true` in the new directory
4. Add template files
5. Run `git add .` and `git commit -m "Add new sample repo"`
6. Run `git config --bool core.bare true`

To update an existing sample repo:
1. Run `git config --bool core.bare false`
2. Make changes to the repo
3. Run `git add .` and `git commit -m "Update sample repo"`
4. Run `git config --bool core.bare true`