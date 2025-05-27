## Run example

```bash
curl -L https://github.com/glitch-vpn/glitch-deps/releases/download/v0.1.9/glitch_deps_v0.1.9_linux_amd64.tar.gz -o glitch_deps_v0.1.9_linux_amd64.tar.gz

tar -xzf glitch_deps_v0.1.9_linux_amd64.tar.gz
rm glitch_deps_v0.1.9_linux_amd64.tar.gz

chmod +x glitch_deps

./glitch_deps install
```

## Expected tree:
```
.
├── GLITCH_DEPS-lock.json
├── GLITCH_DEPS.json
├── README.md
├── bin
│   ├── glitch_deps
│   └── ss
│       ├── sslocal
│       ├── ssmanager
│       ├── ssserver
│       ├── ssservice
│       └── ssurl
├── glitch_deps
├── sources
│   └── awg
│       └── amneziawg-tools-v1.0.20241018.tar.gz
└── tmp
```
