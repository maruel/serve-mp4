# Serves a directory of video files as MP4 over HTTP

Goal: enable playing a local list of video files on a ChromeCast via
[Caddy](https://caddyserver.com).

![ChromeCast](https://raw.githubusercontent.com/wiki/maruel/serve-mp4/chromecast.png)

Requires [ffmpeg/ffprobe](https://ffmpeg.org/) to be installed. To install, run
[./cmd/build-ffmpeg.sh](cmd/build-ffmpeg.sh).

```
go get -u -v github.com/maruel/serve-mp4/cmd/...
serve-mp4 -root /mnt/foo/bar
```

Then setup as a systemd service:
```
./setup.sh /mnt/foo/bar localhost:7999
```


## Fronting with Caddy

Use a [Caddyfile](https://caddyserver.com/docs/caddyfile) to proxy the server
over HTTPS, assuming `example.com` points to your IP address, then disallow any
external access:

```
example.com {
  log / /var/log/caddy/web.log "{when} {status} {remote} {method} {latency} {size} {uri} {>Referer} {>User-Agent}" {
    rotate {
      size 100 # Rotate after 100 MB
      age  120 # Keep log files for 120 days
      keep 100 # Keep at most 100 log files
    }
  }
  errors {
    log /var/log/caddy/web.err {
      size 100 # Rotate after 100 MB
      age  120 # Keep log files for 120 days
      keep 100 # Keep at most 100 log files
    }
  }

  # Only allow local network.
  ipfilter / {
    rule allow
    ip 192.168.1.1
  }

  # If you want to server a base directory.
  root /var/www/html

  # Proxy to serve-mp4.
  proxy /Videos/ localhost:7999 {
    transparent
    without /Videos
  }
}
```


## Probing

`probe-mp4` leverages [text/template](https://golang.org/pkg/text/template/) to
allow printing data from
[vid.Info](https://godoc.org/github.com/maruel/serve-mp4/vid#Info).

Print the duration of a video:

```
probe-mp4 -fmt "{{.Duration}}" foo.avi
```


Print the duration of every videos along codec data:

```
find . -type f -exec probe-mp4 -fmt "{{.Src}}: {{.Duration}} {{.VideoCodec}}/{{.AudioCodec}}/{{.AudioLang}}" {} \;
```
