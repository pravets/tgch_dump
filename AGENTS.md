# tgch_dump — руководство для LLM-агентов

## Контекст проекта

Go-утилита для дампа Telegram-каналов в Markdown-файлы с YAML front-matter и скачиванием медиа. Использует TDLib (C++) через CGO-обёртку `zelenin/go-tdlib`. Никакого HTTP-API, никаких Bot Token — работает как настоящий Telegram-клиент через пользовательский аккаунт.

## Сборка

```bash
# стандартная сборка
CGO_LDFLAGS="-ltde2e -ltdutils" go build -o tgch_dump ./cmd/tgch_dump

# если TDLib установлен в нестандартный путь
CGO_LDFLAGS="-L/custom/lib -ltde2e -ltdutils" go build -o tgch_dump ./cmd/tgch_dump
```

**Требования к окружению:** `CGO_ENABLED=1`, компилятор C/C++, заголовки и статические библиотеки TDLib (устанавливаются в `/usr/local` по умолчанию).

Флаги `-ltde2e -ltdutils` обязательны для TDLib ≥ 1.8.62 — без них линковщик упадёт с `undefined reference`. Библиотека `go-tdlib` v0.7.6 этих флагов в своих CGO-директивах не содержит.

**После каждого редактирования кода** проверяй компиляцию:
```bash
CGO_LDFLAGS="-ltde2e -ltdutils" go build -o tgch_dump ./cmd/tgch_dump
go vet ./...
```

## Структура пакетов

```
cmd/tgch_dump/main.go          — точка входа: CLI-флаги, slog, context, запуск
internal/config/config.go      — Config, Load(); ExpandEnv перед парсингом YAML
internal/auth/auth.go          — NewClient(), interactCLI() (stdin-авторизация)
internal/state/state.go        — ChannelState, Load/Save; файл .state.json, атомарная запись
internal/dumper/dumper.go      — Dumper, DumpAll/DumpChannel/fetchMessages/processMessage/processAlbum
internal/media/downloader.go   — Downloader (семафор), DownloadAll, BuildRequests
internal/converter/markdown.go — PostData, ConvertMessage, Render, FormattedTextToMarkdown
```

## Поток данных

```
config.yaml (+ env vars)
  → config.Load()
  → auth.NewClient()          # TDLib C++, сессия в .tdlib/db
  → dumper.DumpAll(ctx)
      → resolveChannel()      # @username → SearchPublicChat, число → GetChat
      → state.Load()          # .state.json → LastMessageID для инкрементального режима
      → fetchMessages()       # GetChatHistory батчами по 100 (newest → oldest)
          → albumBuffer       # склейка альбомов через границы батчей
          → processMessage()  # или processAlbum() для MediaAlbumId != 0
              → media.BuildRequests()
              → downloader.DownloadAll()  # параллельно, ≤N горутин; TDLib DownloadFile(sync=true)
              → converter.ConvertMessage() + Render()
              → записывает outDir/YYYY-MM-DD_XXXXXXXX.md
      → state.Save()          # атомарно: запись в .tmp → os.Rename → .state.json
```

## Критичные детали реализации

### UTF-16 смещения в TDLib entities
`FormattedTextToMarkdown` кодирует текст в **UTF-16** перед применением entities, потому что TDLib задаёт смещения `Offset`/`Length` в **UTF-16 code units** — не в байтах и не в рунах Go. При любом изменении логики форматирования текста нельзя переходить на байтовые или рунные индексы.

### TDLib messageID ≠ URL-номер поста
`msg.Id` из TDLib — это `postNumber << 20`. Видимый номер поста в URL `t.me/channel/N` — `msg.Id >> 20`. При генерации `telegram_url` в front-matter всегда применяй сдвиг вправо на 20 бит.

Пример: `msg.Id = 423624704` → `423624704 >> 20 = 404` → `https://t.me/channel/404`.

### Порядок сообщений в батче
`GetChatHistory` возвращает сообщения **от новых к старым**. В срезе `albumBuffer.messages` элемент с индексом `[len-1]` — хронологически первый (первым отправленный). Он используется как primary для имени файла, даты и поиска подписи.

### Склейка альбомов через границы батчей
`albumBuffer` не сбрасывается в конце батча — только при смене `albumID` или при встрече обычного сообщения. Так корректно обрабатывается случай, когда альбом разрезан пагинацией.

## Соглашения о файлах

| Артефакт | Шаблон |
|---|---|
| Markdown-пост | `<outDir>/<YYYY-MM-DD>_<msgID:08d>.md` |
| Медиавложение | `<outDir>/attachments/<msgID:08d>_<type>_<index><ext>` |
| Директория канала | `sanitizeName(chat.Title)` — символы `\/:*?"<>\|` и пробелы → `_` |
| Состояние | `<outDir>/.state.json` |

Медиа **всегда** в подпапке `attachments/`. Ссылки в Markdown — относительные: `attachments/00001234_photo_1.jpg`.

## URL каналов в front-matter

- Публичный `@username` → `https://t.me/username/<postNum>`
- Приватный `-1001234567890` → `https://t.me/c/1234567890/<postNum>` (убрать префикс `-100`)

Реализовано в `channelURLBase()` (`internal/dumper/dumper.go`).

## Конфигурация

`config.yaml` поддерживает `${ENV_VAR}` — `os.ExpandEnv` применяется до парсинга YAML (можно хранить API-ключи в переменных окружения). Дефолты задавать в `config.Load()`.

## Graceful shutdown

`signal.NotifyContext` передаётся через весь стек вызовов. `flushPending()` вызывается при любом выходе из цикла, включая ошибки и прерывание по `Ctrl+C`.

## Рецепты расширения

### Добавить новый тип медиа
1. `internal/media/downloader.go` — добавить case в type switch в `BuildRequests`
2. `internal/converter/markdown.go` — добавить case в type switch в `ConvertMessage`
3. `internal/converter/markdown.go` — добавить рендеринг в секцию `for _, m := range pd.Media` в `Render`
4. `internal/config/config.go` — добавить тип в дефолты `media_types` в `Load()`, если нужно включать по умолчанию

### Добавить поле во front-matter
1. Добавить поле в `converter.PostData`
2. Заполнить в `converter.ConvertMessage`
3. Вывести в `converter.writeFrontmatter`
4. При необходимости прокинуть данные через `dumper.processMessage` / `dumper.processAlbum`

## Зависимости

| Пакет | Версия | Назначение |
|---|---|---|
| `github.com/zelenin/go-tdlib` | v0.7.6 | CGO-обёртка TDLib |
| `gopkg.in/yaml.v3` | v3.0.1 | Парсинг конфига |

Не добавляй зависимостей без явной необходимости.

## Тестирование

Автоматических тестов нет. После внесения изменений:
1. Собери проект: `CGO_LDFLAGS="-ltde2e -ltdutils" go build -o tgch_dump ./cmd/tgch_dump`
2. Запусти `go vet ./...`
3. Проверь запуском с реальным конфигом, изучив файлы в `./output/`
4. При изменении `FormattedTextToMarkdown` вручную проверь посты с кириллицей и emoji — они попадают в суррогатные пары UTF-16
