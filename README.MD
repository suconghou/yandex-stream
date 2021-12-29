

## API

GET `/yandex/` to list files

GET `/yandex/some-file.mp4` to stream file


receive

```json
{
    "type":"req",
    "part":0
}
```

```json
{
    "type":"quit"
}
```

send

```json
{
    "type":"error",
    "status":502,
    "msg":"11"
}
```