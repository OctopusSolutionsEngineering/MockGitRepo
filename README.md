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