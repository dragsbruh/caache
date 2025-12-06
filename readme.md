# caache

[coverartarchive](https://coverartarchive.org) proxy that caches images

## installation

```sh
go install github.com/dragsbruh/caache@latest
```

or if you docker

```sh
docker pull ghcr.io/dragsbruh/caache:latest
```

## config

configuration is done via env variables

| variable   | required | default | description                                           |
| ---------- | -------- | ------- | ----------------------------------------------------- |
| IMAGES_DIR | yes      | \-      | directory where images are stored                     |
| ADDR       | no       | :80     | listen address                                        |
| DEV        | no       | \-      | fancier logs and debug log level (enabled if present) |

- cache header is set on cover art responses with max age 365 days
- cached images are permanent until manually removed
- cors is enabled for all get requests

## usage

```sh
IMAGES_DIR=/images ADDR=:8080 caache
```

or

```sh
docker run -d --rm --name caache -v ./images:/images -e IMAGES_DIR=/images -p 8080:80 ghcr.io/dragsbruh/caache:latest
```

or

```yaml
services:
  caache:
    image: ghcr.io/dragsbruh/caache:latest
    restart: unless-stopped

    volumes:
      - ./images:/images

    environment:
      - IMAGES_DIR=/images

    ports:
      - 8080:80
```

### api

similar to the real coverartarchive

```
http://localhost:8080/{record-type}/{mbid}
http://localhost:8080/release/60b529f1-f99b-499f-9b3d-e96f9971039e
```

need to add support for multiple sizes including 128x128, 250x250, 500x500, 1200x1200.
sizes are basically all caa supported + 128x128 because yes. currently only 128x128 is served.

## todo

- [ ] allow multiple resolutions
