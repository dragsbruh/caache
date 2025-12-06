# caache

[**c**over**a**rt**a**rchive](https://coverartarchive.org) proxy that ca**che**s images

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
- cors is enabled for all get and preflight requests
- available resolutions: `128`, `256`, `512`, `1024`, `original`

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

#### `/{release|release-group}/{mbid}/{resolution}`

similar to the real coverartarchive, but with different resolution options

```sh
http://localhost:8080/release/60b529f1-f99b-499f-9b3d-e96f9971039e/256
http://localhost:8080/release/60b529f1-f99b-499f-9b3d-e96f9971039e/1024
http://localhost:8080/release/58a1c084-332d-4012-86af-88b0cf8e47a7/original # original is whatever size is on /front of caa api
```

multiple sizes are available, including `128x128`, `256x256`, `512x512`, `1024x1024`, and `original`.

when an image is not cached in disk, the original (full res) image is
requested from caa and is downscaled to all supported resolutions.

## todo

- [x] allow multiple resolutions
- [ ] support back images too (? idk if thats needed for my personal use but might do it later if im free)
