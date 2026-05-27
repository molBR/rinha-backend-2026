# Round 4 — nginx tuning

## What to change

### nginx/nginx.conf
Replace the full file with:

```nginx
worker_processes 1;
error_log /dev/null crit;

events {
    worker_connections 1024;
    use epoll;
    multi_accept on;
}

http {
    access_log off;
    sendfile on;
    tcp_nopush on;
    tcp_nodelay on;

    upstream api {
        server api1:8080;
        server api2:8080;
        keepalive 128;
        keepalive_requests 10000;
        keepalive_timeout 60s;
    }

    server {
        listen 9999 reuseport;

        location / {
            proxy_pass http://api;
            proxy_http_version 1.1;
            proxy_set_header Connection "";
            proxy_set_header Host "";
            proxy_connect_timeout 1s;
            proxy_read_timeout 3s;
            proxy_send_timeout 3s;
            proxy_buffer_size 4k;
            proxy_buffers 4 4k;
            proxy_busy_buffers_size 8k;
        }
    }
}
```

Key changes:
- `sendfile on` + `tcp_nopush on` + `tcp_nodelay on`: reduce kernel overhead
- `keepalive 128`: more persistent connections (less reconnect overhead)
- `keepalive_requests 10000`: don't close connections after 100 reqs (default)
- `proxy_connect_timeout 1s`: fail fast if backend is dead
- `proxy_buffer_size 4k`: small buffer (our responses are <200 bytes)
- `listen 9999 reuseport`: better load distribution at kernel level

## Note
nginx.conf changes go on the submission branch since it's referenced as a volume:
```yaml
volumes:
  - ./nginx/nginx.conf:/etc/nginx/nginx.conf:ro
```
So update nginx.conf on the submission branch.
