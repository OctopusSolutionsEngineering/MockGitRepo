A mock Git server that create disposable Git repos for testing.

The sample repos exist in `repotemplate.tar.bz2`. This is to prevent Git from complaining about nested Git repositories in the project. 
The `unpacktemplate.sh` and `packtemplate.sh` scripts are used to unpack and pack the template repository.

## Usage

Start the server using the pre-built image from GitHub Container Registry:

```bash
docker run -d --name mockgitserver -p 8080:8080 ghcr.io/mcasperson/mockgitrepo:latest
```

Or with Podman:

```bash
podman run -d --name mockgitserver -p 8080:8080 ghcr.io/mcasperson/mockgitrepo:latest
```

Clone the repository (you must provide a username in the URL):

```bash
cd /tmp
git clone http://myusername@localhost:8080/repo/platformhubrepo
```

## Building Locally

Build the Docker image:

```bash
docker build -t mockgitserver .
```

Run the locally built image:

```bash
docker run -d --name mockgitserver -p 8080:8080 mockgitserver
```

## Using the hosted version

This application is hosted on Azure:

```bash
git clone https://blahblah@mockgitserver.orangegrass-c0938ea8.westus2.azurecontainerapps.io/repo/platformhubrepo
cd platformhubrepo
touch newfile.txt
git add newfile.txt
git commit -m "Add new file to test commit"
git push origin master
```

## CI/CD

The project includes a GitHub Actions workflow that automatically:
- Builds the Docker image for AMD64 and ARM64 architectures
- Pushes the image to GitHub Container Registry (GHCR)
- Tags images appropriately based on branches, tags, and commits
- Runs on push to main/master branches, tags, and pull requests

## Adding new sample repos

1. Run `unpacktemplate.sh` to unpack the template repo into `repotemplate`
2. Add a directory in `repotemplate`
3. Run `git init` in the new directory
4. Run `git config http.receivepack true` in the new directory
5. Add template files
6. Run `git add .` and `git commit -m "Add new sample repo"`
7. Run `git config --bool core.bare true`
8. Run `packtemplate.sh` to pack the template repo into `repotemplate.tar.bz2`

## Update sample repo

1. Run `unpacktemplate.sh` to unpack the template repo into `repotemplate`
2. Run `git config --bool core.bare false`
3. Make changes to the repo
4. Run `git add .` and `git commit -m "Update sample repo"`
5. Run `git config --bool core.bare true`
6. Run `packtemplate.sh` to pack the template repo into `repotemplate.tar.bz2`