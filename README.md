A mock Git server that creates ephemeral Git repos for testing.

Every request is against a fresh copy of a Git repo, so no changes are preserved. 

The service accepts any username and password.

The repo copies are created when they are first accessed, so the workflow is:

1. Clone a repo  (e.g. `git clone http://localhost:8080/repo/platformhubrepo`)
2. The server creates a temporary copy of the git repo for that request.
3. The client can interact with the repo as normal (e.g. `git add`, `git commit`, `git push`), and the temporary directory will be modified.
4. The temporary directory is immediately deleted after the request is complete.

The sample repos exist in `repotemplate.tar.bz2`. This is to prevent Git from complaining about nested Git repositories in the project. 
The `unpacktemplate.sh` and `packtemplate.sh` scripts are used to unpack and pack the template repository.

## Working around the unique git repo limitation in Octopus

Octopus has a restriction that means a git repo can only be used by one project in any space.

This can be worked around by pointing the project to the repo `https://mockgit.octopus.com/uniquerepo/id/projectrepo`, where `<id>` is a unique identifier (e.g. a number). This way, each project can have its own copy of the repo, and they won't conflict with each other.

## Repos

* `platformhubrepo`: A sample Octopus Platform Hub repo
* `projectrepo`: A sample Octopus CaC project configured to use the process template in `platformhubrepo`
* `argocd`: A sample Argo CD project repo.
* `blank#`: Blank repos. Replace `#` with a number between 1 and 10.

Repos are cloned with the command:

```bash
git clone https://<unique user name>@mockgit.octopus.com/repo/<repo name>
```

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

This application is hosted on Azure. The follow commands demonstrate how you can clone and then interact with the repo.

```bash
git clone https://blahblah@mockgit.octopus.com/repo/platformhubrepo
cd platformhubrepo
touch newfile.txt
git add newfile.txt
git commit -m "Add new file to test commit"
git push origin main
git pull
# Git will report a "forced update" with the changes reverted
```

## Adding new sample repos

1. Run `unpacktemplate.sh` to unpack the template repo into `repotemplate`
2. Add a directory in `repotemplate`
3. Run `git init` in the new directory
4. Run `git config http.receivepack true` in the new directory
5. Add template files
6. Run `git add .` and `git commit -m "Add new sample repo"`
7. Run `git checkout -b main` to create the main branch
8. Run `git config --bool core.bare true`
9. Run `git config receive.denyNonFastForwards false`
10. Run `packtemplate.sh` to pack the template repo into `repotemplate.tar.bz2`

## Update sample repo

1. Run `unpacktemplate.sh` to unpack the template repo into `repotemplate`
2. Run `git config --bool core.bare false`
3. Make changes to the repo
4. Run `git add .` and `git commit -m "Update sample repo"`
5. Run `git config --bool core.bare true`
6. Run `packtemplate.sh` to pack the template repo into `repotemplate.tar.bz2`

These are the commands chained up:

```bash
git config --bool core.bare false; git add .; git commit -m "Update sample repo"; git config --bool core.bare true; cd ../..
```

## Persisting credentials

```bash
curl -X PUT -H "X_MOCKGIT_SERVICE_API_KEY: tokengoeshere" -d '{"data":{"type: "credentials", "id": "user1", "attributes": {"password": "blah"}}}' http://localhost:8080/api/credentials 
```