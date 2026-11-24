# Dokumentasi API Wilayah Indonesia (v1)

API read-only untuk hierarki wilayah administrasi Indonesia (provinsi, kabupaten/kota, kecamatan, desa/kelurahan), kodepos, dan pulau. Semua endpoint memakai metode `GET` (dan `HEAD`), tanpa autentikasi.

Spesifikasi mesin-terbaca ada di [`api/openapi.yaml`](../api/openapi.yaml) (OpenAPI 3.1). Saat service berjalan, spesifikasi yang sama tersedia di `/docs/openapi.yaml` dan ringkasannya di `/docs`.

Untuk tampilan interaktif (Swagger UI), buka `/docs/openapi.yaml` lalu tempel isinya di <https://editor.swagger.io>, atau jalankan lokal:

```bash
docker run --rm -p 8081:8080 -e SWAGGER_JSON=/spec/openapi.yaml \
  -v "$PWD/api:/spec" swaggerapi/swagger-ui
```

Contoh di bawah memakai `https://wilayah.contoh.id`. Ganti dengan domain deploy Anda.

## Ringkasan endpoint

| Endpoint | Fungsi | Paginasi |
|---|---|---|
| `GET /v1/meta` | Versi data, dasar hukum, jumlah per level, hash sumber | Tidak |
| `GET /v1/provinsi` | Semua provinsi (38) | Tidak |
| `GET /v1/provinsi/{kode}/kabupaten` | Kabupaten/kota dalam provinsi | Tidak |
| `GET /v1/kabupaten/{kode}/kecamatan` | Kecamatan dalam kabupaten/kota | Ya |
| `GET /v1/kecamatan/{kode}/desa` | Desa/kelurahan dalam kecamatan | Ya |
| `GET /v1/wilayah/{kode}` | Satu wilayah di level mana pun, lengkap dengan rantai induk | Tidak |
| `GET /v1/wilayah/{kode}/pulau` | Pulau di provinsi atau kabupaten/kota | Ya |
| `GET /v1/kodepos/{kodepos}` | Semua desa/kelurahan dengan kodepos itu | Ya |
| `GET /v1/cari` | Pencarian nama wilayah | Ya |
| `GET /healthz` | Cek kesehatan service | Tidak |

Polygon batas wilayah (boundaries) belum tersedia di v1 dan direncanakan di v1.1.

## Konvensi

### Format kode wilayah

Kode memakai format bertitik sesuai Kepmendagri:

| Level | Nama | Format | Contoh |
|---|---|---|---|
| 1 | Provinsi | `xx` | `11` |
| 2 | Kabupaten/kota | `xx.xx` | `11.01` |
| 3 | Kecamatan | `xx.xx.xx` | `11.01.01` |
| 4 | Desa/kelurahan | `xx.xx.xx.xxxx` | `11.01.01.2001` |

Kode pulau berbentuk `xx.xx.4xxxx` (contoh `11.01.40001`). Pulau yang kode kabupatennya `00` (`xx.00.4xxxx`) dihitung milik provinsi.

Kode berformat salah dijawab `400`. Kode berformat benar tapi tidak ada dijawab `404`.

### Paginasi

Endpoint berpaginasi menerima:

| Parameter | Arti | Default | Maksimum |
|---|---|---|---|
| `limit` | Jumlah item per halaman | 100 (20 untuk `/cari`) | 500 (100 untuk `/cari`) |
| `cursor` | Nilai dari `page.next_cursor` halaman sebelumnya | halaman pertama | |

Respons berisi objek `page`:

```json
"page": { "limit": 2, "total": 18, "next_cursor": "bzoy" }
```

- `next_cursor` ada selama masih ada halaman berikutnya. Ambil nilainya apa adanya, jangan diubah atau dibangun sendiri.
- `total` ada di endpoint hierarki dan pulau, tidak ada di `/cari`.
- Cursor yang melewati akhir hasil: di `/cari` dijawab `400`. Di endpoint lain, cursor tepat di akhir dijawab `200` dengan list kosong.

### Cache

Semua respons data membawa:

| Header | Isi |
|---|---|
| `ETag` | Penanda versi respons |
| `Cache-Control` | `public, max-age=3600` |
| `X-Data-Version` | Versi dataset, sama dengan `data_version` di `/v1/meta` |

Kirim `If-None-Match: <ETag>` untuk menerima `304 Not Modified` tanpa body bila data tidak berubah.

### Rate limit

Pembatasan per alamat IP klien, default 10 request per detik dengan burst 20. Nilai di deploy Anda bisa berbeda. `/healthz` tidak dibatasi. Bila terlampaui, server menjawab `429` dengan header `Retry-After` (detik).

### CORS

Secara default semua origin diizinkan (`Access-Control-Allow-Origin: *`) dan respons `429` ikut membawa header CORS, sehingga aplikasi browser dapat membaca `Retry-After`. Operator dapat membatasinya lewat `CORS_ORIGINS`.

### Format error

Semua error memakai `application/problem+json` (RFC 9457):

```json
{
  "type": "about:blank",
  "title": "Not Found",
  "status": 404,
  "detail": "wilayah 99 not found",
  "request_id": "93a953dcef692f53"
}
```

Sertakan `request_id` saat melaporkan masalah ke operator.

| Status | Arti |
|---|---|
| `400` | Parameter, kode, atau cursor tidak valid |
| `404` | Wilayah, kodepos, atau route tidak ada |
| `405` | Metode selain `GET`/`HEAD`. Header `Allow` menyebut yang didukung |
| `429` | Rate limit terlampaui |
| `503` | `/cari` melewati batas waktu request |

## Endpoint

### `GET /v1/meta`

Informasi dataset.

```bash
curl https://wilayah.contoh.id/v1/meta
```

```json
{
  "data_version": "2026-10-08",
  "build_date": "2026-11-24T15:21:32Z",
  "dasar_kepmendagri": "Kepmendagri No 300.2.2-2138 Tahun 2025",
  "catatan": "Label dasar diambil dari header wilayah.sql ...",
  "counts": { "desa": 83762, "kabupaten": 514, "kecamatan": 7285, "provinsi": 38, "pulau": 17374 },
  "sources": [
    { "file": "wilayah.sql", "sha256": "c4c3...4031", "kepmendagri": "Kepmendagri No 300.2.2-2138 Tahun 2025" },
    { "file": "wilayah_kodepos.sql", "sha256": "7ddc...8b2c", "kepmendagri": "Kepmendagri No 300.2.2-2138 Tahun 2025" }
  ]
}
```

`data_version` mengikuti tanggal "last edit" terbaru di sumber data, bukan tanggal build. Label dasar hukum mengikuti isi data yang diverifikasi. Bidang `catatan` menjelaskan selisih label antar file sumber.

### `GET /v1/provinsi`

Semua provinsi. Level 1 dan 2 membawa objek `detail`.

```bash
curl https://wilayah.contoh.id/v1/provinsi
```

```json
{
  "data": [
    {
      "kode": "11",
      "nama": "Aceh",
      "level": 1,
      "detail": {
        "ibukota": "Banda Aceh",
        "lat": 5.570546962920454,
        "lng": 95.34080851187178,
        "elv": 11,
        "tz": 7,
        "luas": 56835.019,
        "penduduk": 5623479,
        "status": 1
      }
    }
  ]
}
```

Arti bidang `detail`: `elv` ketinggian (meter), `tz` zona waktu (jam dari UTC, 7 = WIB), `luas` dalam km², `penduduk` jumlah jiwa, `status` kode status dari data sumber.

### `GET /v1/provinsi/{kode}/kabupaten`

Kabupaten/kota dalam satu provinsi. Bentuk respons sama dengan `/v1/provinsi`, `level` bernilai 2.

```bash
curl https://wilayah.contoh.id/v1/provinsi/11/kabupaten
```

### `GET /v1/kabupaten/{kode}/kecamatan`

Kecamatan dalam kabupaten/kota. Berpaginasi.

```bash
curl 'https://wilayah.contoh.id/v1/kabupaten/11.01/kecamatan?limit=2'
```

```json
{
  "data": [
    { "kode": "11.01.01", "nama": "Bakongan", "level": 3 },
    { "kode": "11.01.02", "nama": "Kluet Utara", "level": 3 }
  ],
  "page": { "limit": 2, "total": 18, "next_cursor": "bzoy" }
}
```

Halaman berikutnya: `curl 'https://wilayah.contoh.id/v1/kabupaten/11.01/kecamatan?limit=2&cursor=bzoy'`.

### `GET /v1/kecamatan/{kode}/desa`

Desa/kelurahan dalam kecamatan, lengkap dengan `kodepos`. Berpaginasi.

```bash
curl 'https://wilayah.contoh.id/v1/kecamatan/11.01.01/desa?limit=2'
```

```json
{
  "data": [
    { "kode": "11.01.01.2001", "nama": "Keude Bakongan", "level": 4, "kodepos": "23773" },
    { "kode": "11.01.01.2002", "nama": "Ujong Mangki", "level": 4, "kodepos": "23773" }
  ],
  "page": { "limit": 2, "total": 7, "next_cursor": "bzoy" }
}
```

### `GET /v1/wilayah/{kode}`

Satu wilayah di level mana pun. Bidang `induk` berisi rantai induk, dari provinsi ke bawah. Untuk provinsi, `induk` berupa array kosong.

```bash
curl https://wilayah.contoh.id/v1/wilayah/11.01.01.2001
```

```json
{
  "kode": "11.01.01.2001",
  "nama": "Keude Bakongan",
  "level": 4,
  "kodepos": "23773",
  "induk": [
    { "kode": "11", "nama": "Aceh", "level": 1 },
    { "kode": "11.01", "nama": "Kabupaten Aceh Selatan", "level": 2 },
    { "kode": "11.01.01", "nama": "Bakongan", "level": 3 }
  ]
}
```

Untuk level 1 dan 2, respons memuat `detail`. Untuk level 4, respons memuat `kodepos`.

### `GET /v1/wilayah/{kode}/pulau`

Pulau di provinsi atau kabupaten/kota (level 1 atau 2). Berpaginasi.

```bash
curl 'https://wilayah.contoh.id/v1/wilayah/11.01/pulau?limit=2'
```

```json
{
  "data": [
    { "kode": "11.01.40001", "nama": "Pulau Batukapal", "lat": 3.3176, "lng": 97.1283, "status": "TBP", "luas": 0.0006 },
    { "kode": "11.01.40002", "nama": "Pulau Batutunggal", "lat": 3.4153, "lng": 97.0725, "status": "TBP", "luas": 0.0078 }
  ],
  "page": { "limit": 2, "total": 6, "next_cursor": "bzoy" }
}
```

`luas` dalam km². `status` adalah kode status dari data sumber. Bidang `notes` ikut muncul bila sumber mengisinya.

### `GET /v1/kodepos/{kodepos}`

Semua desa/kelurahan dengan kodepos tertentu. `kodepos` harus 5 digit. Berpaginasi.

```bash
curl https://wilayah.contoh.id/v1/kodepos/23773
```

```json
{
  "data": [
    { "kode": "11.01.01.2001", "nama": "Keude Bakongan", "level": 4, "kodepos": "23773" },
    { "kode": "11.01.01.2002", "nama": "Ujong Mangki", "level": 4, "kodepos": "23773" }
  ],
  "page": { "limit": 100, "total": 7 }
}
```

Data kodepos hanya diperiksa kecocokan kodenya dengan data wilayah, bukan kebenaran kodepos itu sendiri.

### `GET /v1/cari`

Pencarian nama wilayah, tidak membedakan huruf besar kecil. Hasil yang diawali `q` muncul lebih dulu, lalu yang mengandung `q`.

| Parameter | Wajib | Keterangan |
|---|---|---|
| `q` | Ya | 2 sampai 64 karakter |
| `level` | Tidak | `1`–`4` atau `provinsi`, `kabupaten`, `kecamatan`, `desa` |
| `limit` | Tidak | 1–100, default 20 |
| `cursor` | Tidak | Dari `page.next_cursor` |

```bash
curl 'https://wilayah.contoh.id/v1/cari?q=bakongan&level=kecamatan&limit=2'
```

```json
{
  "data": [
    { "kode": "11.01.01", "nama": "Bakongan", "level": 3 },
    { "kode": "11.01.15", "nama": "Bakongan Timur", "level": 3 }
  ],
  "page": { "limit": 2, "next_cursor": "bzoy" }
}
```

Batasan khusus `/cari`:
- Kedalaman paginasi dibatasi. Cursor melewati sekitar 1000 hasil pertama dijawab `400`, dan `next_cursor` tidak diterbitkan lagi di batas itu. Persempit pencarian dengan `level` atau `q` yang lebih spesifik.
- Pencarian yang melewati batas waktu request dijawab `503`.

### `GET /healthz`

```bash
curl https://wilayah.contoh.id/healthz
```

```json
{ "status": "ok" }
```

## Contoh penggunaan

**Dropdown bertingkat (provinsi, kabupaten, kecamatan, desa):**

```bash
curl https://wilayah.contoh.id/v1/provinsi
curl https://wilayah.contoh.id/v1/provinsi/11/kabupaten
curl https://wilayah.contoh.id/v1/kabupaten/11.01/kecamatan
curl https://wilayah.contoh.id/v1/kecamatan/11.01.01/desa
```

**Alamat lengkap dari kode desa:** satu request ke `/v1/wilayah/{kode}` sudah mengembalikan desa beserta kabupaten, kecamatan, dan provinsinya lewat `induk`.

**Isi otomatis dari kodepos:** `/v1/kodepos/{kodepos}` mengembalikan daftar desa. Gabungkan dengan `/v1/wilayah/{kode}` untuk mendapat rantai induk tiap desa.

**Mengambil semua halaman:**

```bash
cursor=""
while :; do
  r=$(curl -s "https://wilayah.contoh.id/v1/kodepos/23773?limit=100${cursor:+&cursor=$cursor}")
  echo "$r" | jq -c '.data[]'
  cursor=$(echo "$r" | jq -r '.page.next_cursor // empty')
  [ -z "$cursor" ] && break
done
```

Hormati `429`: bila menerima `Retry-After`, tunggu sebanyak detik itu sebelum mengulang.
