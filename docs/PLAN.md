# Rencana: API Wilayah Indonesia (Go + chi)

Status: tahap 0–6 selesai dan lolos review (2026-11-24); belum di-commit dan belum dideploy. Boundaries (v1.1) belum dikerjakan. Disusun 2026-11-24. Keputusan terbuka diselesaikan 2026-11-24 (lihat bagian 10). Catatan pelaksanaan di bagian 11.

## 1. Tujuan dan batas

- Service **read-only** yang menyajikan hierarki provinsi → kab/kota → kecamatan → desa/kelurahan, kodepos, dan data pendukung (koordinat, luas, penduduk, pulau).
- Dipakai banyak sistem, jadi API harus stabil, punya versi, dan terdokumentasi.
- **Tidak ada database di runtime.** Data dimuat ke memori saat start.
- Di luar lingkup versi 1: pencarian fuzzy canggih, endpoint tulis, UI admin, boundaries (polygon), API key, dan banyak versi data.

## 2. Sumber data

| Data | Sumber | Isi |
|---|---|---|
| Nama dan hierarki | `wilayah/db/wilayah.sql` | `kode`, `nama` (91.599 baris, 4 level) |
| Kodepos | `wilayah_kodepos/db/wilayah_kodepos.sql` | `kode`, `kodepos` (83.762 desa) |
| Detail level 1–2 | `wilayah/db/wilayah_level_1_2.sql` | `ibukota`, `lat`, `lng`, `elv`, `tz`, `luas`, `penduduk`, `path` (polygon), `status` |
| Pulau | `wilayah/db/wilayah_pulau.sql` | `kode`, `nama`, `lat`, `lng`, `status`, `luas`, `notes` |
| Luas dan penduduk | `wilayah_luas.sql`, `wilayah_penduduk.sql` | Opsional, sebagian sudah ada di `level_1_2` |

Repo sumber:
- https://github.com/cahyadsn/wilayah
- https://github.com/cahyadsn/wilayah_kodepos
- https://github.com/cahyadsn/wilayah_ref (arsip PDF peraturan, bukan data)

Catatan dari pemeriksaan:
- Kode wilayah di `wilayah` dan `wilayah_kodepos` cocok 100%: 83.762 desa, 7.285 kecamatan, 514 kab/kota, 38 provinsi, tanpa baris yatim. Yang dicek hanya kode, bukan nama atau kebenaran kodepos.
- `wilayah.sql` dan `wilayah_kodepos.sql` memakai format `INSERT` yang berbeda. Parser harus menangani keduanya, termasuk escape tanda kutip di nama.
- `level_1_2.sql` berukuran 22 MB karena kolom `path`. Kolom ini **dipisah** dari data utama.
- Header `wilayah.sql` masih menyebut Kepmendagri 300.2.2-2138, sementara README menyebut 300.2.2-2430. Kepmendagri 2430 adalah perubahan atas 2138 (ditetapkan 23 Juni 2025), dan changelog `wilayah` mencatatnya hanya untuk kode pulau. Versi data di API mengikuti isi data yang diverifikasi, bukan klaim README.
- Repo `wilayah` terus memperbaiki nama setelah Juni 2025, jadi nama harus diambil dari salinan terbaru.

## 3. Pipeline build data

Satu perintah `go run ./cmd/builddata` mengubah sumber SQL menjadi artefak yang di-embed:

1. Parse SQL dari folder sumber (path lewat flag, tidak di-hardcode).
2. Validasi:
   - format kode `xx`, `xx.xx`, `xx.xx.xx`, `xx.xx.xx.xxxx`;
   - setiap induk ada;
   - jumlah per level sesuai (38 / 514 / 7.285 / 83.762);
   - kodepos 5 digit;
   - join nama dan kodepos tanpa baris yatim.
3. Tulis `data/wilayah.json.gz` (nama, hierarki, kodepos, detail ringan). Polygon (`boundaries.*`) ditunda ke v1.1.
4. Catat metadata: tanggal build, hash sumber, dan label versi data.

Hasilnya di-commit ke repo supaya build service tidak bergantung pada repo sumber.

## 4. Arsitektur

```
cmd/api/            entrypoint server
cmd/builddata/      parser dan validator data
internal/data/      load dari embed, struktur dan indeks di memori
internal/api/       router chi, handler, middleware
internal/model/     tipe Wilayah, Kodepos, Pulau
data/               artefak hasil build (embed)
api/openapi.yaml    spesifikasi API
docs/PLAN.md        dokumen ini
```

Struktur di memori:
- `map[kode]*Wilayah` untuk lookup langsung.
- Slice anak per induk, supaya endpoint hierarki O(1).
- `map[kodepos][]kode` untuk pencarian balik dari kodepos.
- Indeks nama (lowercase, prefix) untuk pencarian.
- Struktur dibuat sekali saat start lalu **immutable**, jadi tidak perlu lock.

Boundaries ditunda ke v1.1. Kolom `path` tidak ikut diproses di v1. Saat v1.1, polygon dibaca saat dibutuhkan dari file atau SQLite terpisah, lalu di-cache.

## 5. Endpoint (v1, prefix `/v1`)

| Endpoint | Fungsi |
|---|---|
| `GET /provinsi` | Daftar 38 provinsi |
| `GET /provinsi/{kode}/kabupaten` | Kab/kota dalam provinsi |
| `GET /kabupaten/{kode}/kecamatan` | Kecamatan dalam kab/kota |
| `GET /kecamatan/{kode}/desa` | Desa/kelurahan dalam kecamatan |
| `GET /wilayah/{kode}` | Detail satu wilayah (semua level), termasuk rantai induk |
| `GET /kodepos/{kodepos}` | Semua desa dengan kodepos itu |
| `GET /cari?q=&level=&limit=` | Pencarian nama, dengan batas hasil |
| `GET /wilayah/{kode}/pulau` | Pulau di kab/kota atau provinsi |
| `GET /meta` | Versi data, dasar Kepmendagri, jumlah per level, tanggal build |
| `GET /healthz`, `GET /docs` | Kesehatan service dan dokumentasi OpenAPI |

Konvensi:
- Respons JSON, paginasi dengan `limit` dan `cursor` untuk daftar besar.
- Kode memakai format bertitik sesuai Kepmendagri. Kode tidak valid dijawab `400`, kode tidak ada dijawab `404`.
- Format error seragam (RFC 9457 problem+json).
- `ETag` dan `Cache-Control` pada semua respons data. Header `X-Data-Version` menyebut versi data.

## 6. Keamanan dan operasional

- Akses publik tanpa API key. Rate limit per IP. Service berjalan di belakang proxy Coolify, jadi IP asli dibaca dari `X-Forwarded-For` hanya bila request datang dari proxy tepercaya (daftar CIDR lewat konfigurasi). Selain itu pakai IP koneksi.
- Middleware chi: request ID, logging terstruktur, recover, timeout, CORS yang bisa dikonfigurasi, batas panjang `q`.
- Konfigurasi lewat environment variable (port, CIDR proxy tepercaya, batas rate limit, CORS). Tidak ada secret di repo.
- Graceful shutdown.
- Metrik dasar (jumlah request, latensi) lewat endpoint terpisah yang tidak publik.

## 7. Pengujian

- **Unit:** parser SQL, validasi kode, indeks pencarian.
- **Data:** hitungan per level, tidak ada baris yatim, kodepos valid.
- **Handler:** `httptest` untuk semua endpoint, termasuk 400, 404, dan ETag (304).
- **Kontrak:** respons dicocokkan dengan `openapi.yaml`.
- **Benchmark:** `go test -bench` untuk lookup dan pencarian. Target latensi ditetapkan setelah pengukuran.

## 8. Deploy

- Dockerfile multi-stage, image akhir `scratch` atau `distroless`, user non-root.
- CI: `go vet`, `staticcheck`, `go test -race`, build data, build image.
- Deploy ke VPS sendiri lewat Coolify, sama seperti project lain. Repo menyediakan `Dockerfile` dan `docker-compose.yml`. TLS dan reverse proxy ditangani Coolify (Traefik), jadi tidak perlu Caddy atau nginx. Kompresi respons diaktifkan di Traefik atau middleware.
- Rilis data baru = build ulang artefak, naikkan versi data, deploy ulang. Hanya satu versi data aktif. Versi lama dapat dipulihkan lewat tag git dan image sebelumnya.

## 9. Tahapan kerja

| Tahap | Isi | Hasil | Status |
|---|---|---|---|
| 0 | Inisialisasi modul, struktur folder, CI kosong | Repo siap | Selesai |
| 1 | Pipeline build data dan validasi | `data/` terverifikasi | Selesai |
| 2 | Loader memori dan indeks | Lookup hierarki dan kodepos | Selesai |
| 3 | Endpoint inti (provinsi sampai desa, `wilayah/{kode}`, `kodepos`, `meta`) | API dasar jalan | Selesai |
| 4 | Pencarian dan pulau | Fitur v1 lengkap (boundaries ke v1.1) | Selesai |
| 5 | Rate limit per IP (trusted proxy), ETag, error seragam | Siap dipakai sistem lain | Selesai |
| 6 | OpenAPI, Dockerfile + docker-compose untuk Coolify, CI penuh, benchmark | Siap deploy | Selesai, belum diuji di Coolify |
| v1.1 | Endpoint boundary (polygon level 1–2, GeoJSON) dan penyimpanan terpisahnya | Boundaries | Belum |


## 10. Keputusan

Diputuskan 2026-11-24:

1. **Akses:** publik tanpa API key, rate limit per IP. Tahap 5 tidak lagi memuat API key. Bila nanti perlu key, ditambah sebagai perubahan terpisah.
2. **Boundaries:** ditunda ke v1.1. V1 cukup koordinat, ibukota, dan luas.
3. **Versi data:** satu versi aktif. Metadata lewat `/meta` dan `X-Data-Version`.
4. **Tempat deploy:** VPS sendiri dengan Coolify, memakai `Dockerfile` dan `docker-compose.yml`.
5. **Repo sumber:** artefak hasil build disalin ke repo ini. Hash sumber dicatat di metadata.

Keputusan terbuka: tidak ada.

## 11. Catatan pelaksanaan (2026-11-24)

Hasil verifikasi: `go build`, `go vet`, `staticcheck`, `go test -race` (38 test) bersih. `builddata` dari sumber asli menghasilkan 38 provinsi, 514 kab/kota, 7.285 kecamatan, 83.762 desa, 17.374 pulau, tanpa baris yatim. Image Docker berhasil dibuild dan jalan sebagai nonroot. Kode ditinjau oleh `code-reviewer` dan `security-reviewer`, lalu temuannya diperbaiki.

Penyimpangan dari rencana awal:
- Kode pulau `xx.00.4xxxx` (bagian kab/kota "00") dianggap milik provinsi. Tanpa ini 56 pulau gagal validasi sebagai yatim.
- Versi data default adalah tanggal "last edit" terbaru di header sumber, bukan tanggal build. Dasar hukum tetap Kepmendagri 2138; `meta.catatan` menyebut bahwa header `wilayah_pulau.sql` menyebut 2430.
- `/cari`: `q` 2–64 karakter, `limit` default 20 maksimum 100, offset cursor maksimum 1000. Cursor yang melewati akhir hasil dijawab 400. Di endpoint lain, offset tepat di akhir dijawab 200 dengan list kosong.
- Kabupaten dan provinsi tidak dipaginasi; kecamatan, desa, kodepos, cari, dan pulau dipaginasi (cursor opak berbasis offset).
- Rate limit buatan sendiri (token bucket, IPv6 per /64, `/healthz` dikecualikan). `RATE_LIMIT_RPS` minimal 0,01 atau 0 untuk menonaktifkan. Metrik di listener terpisah, default `127.0.0.1:9090`.
- `Allow` dan `Access-Control-Allow-Methods` hanya menyebut GET dan HEAD. Default `CORS_ORIGINS=*`.
- Binary API punya flag `-healthcheck` karena image distroless tidak punya shell.
- Base image di `Dockerfile` di-pin ke digest dan perlu di-bump manual untuk patch keamanan.

Belum selesai atau belum diuji:
- Job CI di GitHub Actions belum pernah jalan. Action `checkout@v4` dan `setup-go@v5` belum di-pin ke SHA.
- `govulncheck` hanya ada di CI, belum dijalankan lokal.
- `docker-compose.yml` dan `TRUSTED_PROXY_CIDRS` belum dicoba dengan Traefik Coolify. Variabel itu harus diisi dengan IP Traefik, kalau tidak semua klien berbagi satu bucket rate limit.
- Belum ada commit git.
