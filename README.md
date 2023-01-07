# Simple Build System

## Installation

### Create a user/password for the password secured area

Run 

```bash
htpasswd -nb oli the-very-secret-password
```

and replace the result into [redeploy/etc/htpass-redeploy]()

### Define your systems and its access passwords

Edit the file [redeploy/etc/redeploy.conf]() with your systems and their rebuild passwords.

> Example:
>
> If you have defined [app-b]=123 you can trigger a rebuild via:
> http://localhost:8080/cgi-bin/redeploy/exec?authorization=123&type=app-b
>

### Define the build and test scripts

For each key defined in [redeploy/etc/redeploy.conf]() you have to have corresponding directoy in `/opt` with at least a `build.sh`
Optionally you can define `test.sh`, `get-git-hash.sh` and `get-git-url.sh`.

* The `build.sh` file should execute your build.
* The optional `test.sh`file should execute your tests.
* The optional, but recommended `get-git-url.sh` file simply outputs your souruce repository URL.
* The optional, but recommended `get-git-hash.sh`file outputs the last commit hash.

See [opt/app-a]() for an example.

## Test, run and integrate

Build a docker image via:

```bash
docker build --tag redeploy .
```

Run it via:

```bash
docker run --rm -d -p 8080:80 --name redeploy redeploy
```

Now you can access the overview page at:

http://localhost:8080/cgi-bin/redeploy/secured/index

User: oli
Password: oli

You can either trigger a build on this page or execute [http://localhost:8080/cgi-bin/redeploy/exec?authorization=123&type=app-b]() from your build pipeline.
