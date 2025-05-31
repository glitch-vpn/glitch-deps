## Run example

```bash
curl -L https://github.com/glitch-vpn/fracture/releases/download/v0.1.10/fracture_v0.1.10_linux_amd64.tar.gz -o fracture_v0.1.10_linux_amd64.tar.gz

tar -xzf fracture_v0.1.10_linux_amd64.tar.gz
rm fracture_v0.1.10_linux_amd64.tar.gz

chmod +x fracture

./fracture install
```

## Expected tree:
```
.
├── fracture-lock.json
├── fracture.json
├── README.md
├── bin
│   ├── fracture
│   └── ss
│       ├── sslocal
│       ├── ssmanager
│       ├── ssserver
│       ├── ssservice
│       └── ssurl
├── fracture
├── sources
│   └── awg
│       └── amneziawg-tools-v1.0.20241018.tar.gz
└── tmp
```
