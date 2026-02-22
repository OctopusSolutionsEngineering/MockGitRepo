A mock Git server that create disposable Git repos for testing.

Each user gets a unique copy of the sample repositories. The service accepts any username and password.

The repo copies are created when they are first accessed, so the workflow is:

1. Clone a repo with a unique username (e.g. `git clone http://myusername@localhost:8080/repo/platformhubrepo`)
2. The server creates a user specific copy of the repo for `myusername` and serves it to the client
3. The client can interact with the repo as normal (e.g. `git add`, `git commit`, `git push`), and the changes will be stored in the user specific copy of the repo
4. After 90 minutes, the repo is deleted and the next time the user accesses the repo, a new copy will be created from the template

The sample repos exist in `repotemplate.tar.bz2`. This is to prevent Git from complaining about nested Git repositories in the project. 
The `unpacktemplate.sh` and `packtemplate.sh` scripts are used to unpack and pack the template repository.

## Using for Octopus Demos

1. Configure the platform hub repo to https://mockgitserver.orangegrass-c0938ea8.westus2.azurecontainerapps.io/repo/platformhubrepo
2. Use a unique username and any password
3. Publish and share the process templates
4. Create a CaC project pointing to https://mockgitserver.orangegrass-c0938ea8.westus2.azurecontainerapps.io/repo/projectrepo
5. Use a unique username and any password
6. You now how a sample platform hub and sample project that can be edited and committed to without affecting other users. The repos will be automatically cleaned up after 90 minutes of inactivity.

## Repos

* `platformhubrepo`: A sample Octopus Platform Hub repo
* `projectrepo`: A sample Octopus CaC project configured to use the process template in `platformhubrepo`
* `blank#`: Blank repos. Replace `#` with a number between 1 and 10.

Repos are cloned with the commane:

```bash
git clone https://<unique user name>@mockgitserver.orangegrass-c0938ea8.westus2.azurecontainerapps.io/repo/<repo name>
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
git clone https://blahblah@mockgitserver.orangegrass-c0938ea8.westus2.azurecontainerapps.io/repo/platformhubrepo
cd platformhubrepo
touch newfile.txt
git add newfile.txt
git commit -m "Add new file to test commit"
git push origin master
```

## Adding new sample repos

1. Run `unpacktemplate.sh` to unpack the template repo into `repotemplate`
2. Add a directory in `repotemplate`
3. Run `git init` in the new directory
4. Run `git config http.receivepack true` in the new directory
5. Add template files
6. Run `git add .` and `git commit -m "Add new sample repo"`
7. Run `git checkout -b main` to create the main branch
7. Run `git config --bool core.bare true`
8. Run `packtemplate.sh` to pack the template repo into `repotemplate.tar.bz2`

## Update sample repo

1. Run `unpacktemplate.sh` to unpack the template repo into `repotemplate`
2. Run `git config --bool core.bare false`
3. Make changes to the repo
4. Run `git add .` and `git commit -m "Update sample repo"`
5. Run `git config --bool core.bare true`
6. Run `packtemplate.sh` to pack the template repo into `repotemplate.tar.bz2`