# Telegram Channel Dumper

![CodeRabbit Pull Request Reviews](https://img.shields.io/coderabbit/prs/github/pravets/tgch_dump?utm_source=oss&utm_medium=github&utm_campaign=pravets%2Ftgch_dump&labelColor=171717&color=FF570A&link=https%3A%2F%2Fcoderabbit.ai&label=CodeRabbit+Reviews)
![License](https://img.shields.io/github/license/pravets/tgch_dump)
[![Telegram](https://telegram-badge.vercel.app/api/telegram-badge?channelId=@pravets_IT)](https://t.me/+GKGRmAwghxllYzIy)

Утилита командной строки для сохранения содержимого Telegram-каналов в набор Markdown-файлов с YAML front-matter, а также сопутствующих медиафайлов (фото, видео, аудио, документы).

Написана на [Go](https://go.dev) с использованием [TDLib](https://core.telegram.org/tdlib) через [zelenin/go-tdlib](https://github.com/zelenin/go-tdlib).

---

## Возможности

- Дампит **публичные и приватные** каналы (для приватных требуется членство в канале)
- Один `.md`-файл на пост с **YAML front-matter** (ID, дата, просмотры, пересылки, реакции, комментарии, данные опросов…)
- Скачивает **фото, видео, аудио, голосовые сообщения, документы, анимации**
- Конвертирует богатое форматирование Telegram (жирный, курсив, код, ссылки, цитаты…) в Markdown
- **Инкрементальный режим** — повторный запуск загружает только новые посты
- **Параллельная загрузка медиа** с настраиваемым пулом воркеров
- Корректное завершение по `Ctrl+C` — текущий батч дообрабатывается, состояние сохраняется
- Медиавложения сохраняются в подпапку `attachments/`, чтобы не засорять основную директорию
- Посты с несколькими вложениями (альбомы) объединяются в один Markdown-файл
- Имя файла начинается с даты (`YYYY-MM-DD_XXXXXXXX.md`) для удобной сортировки
- Ссылка на оригинальный пост в Telegram добавляется в front-matter (`telegram_url`)

---

## Требования

### 1 – TDLib

tgch_dump компонуется с TDLib как с разделяемой библиотекой. Необходимо собрать или установить её заранее.

#### Linux (Ubuntu / Debian)

```bash
sudo apt-get install -y build-essential cmake gperf libssl-dev zlib1g-dev

git clone https://github.com/tdlib/td.git
cd td
mkdir build && cd build
cmake -DCMAKE_BUILD_TYPE=Release ..
make -j$(nproc)
sudo make install      # устанавливает в /usr/local по умолчанию
```

#### macOS (Homebrew)

```bash
brew install tdlib
```

### 2 – Учётные данные Telegram API

1. Перейдите на <https://my.telegram.org/apps>
2. Войдите в свой аккаунт Telegram
3. Создайте приложение и скопируйте **API ID** и **API Hash**

---

## Установка

```bash
git clone https://github.com/pravets/tgch_dump.git
cd tgch_dump
go build -o tgch_dump ./cmd/tgch_dump
```

> На Linux может потребоваться установить `CGO_LDFLAGS`, если TDLib установлена
> в нестандартный путь:
> ```bash
> CGO_LDFLAGS="-L/path/to/tdlib/lib" go build -o tgch_dump ./cmd/tgch_dump
> ```
>
> Если TDLib версии 1.8.62 или новее, также укажите:
> ```bash
> CGO_LDFLAGS="-ltde2e -ltdutils" go build -o tgch_dump ./cmd/tgch_dump
> ```

---

## Конфигурация

Скопируйте пример конфига и заполните свои данные:

```bash
cp config.example.yaml config.yaml
$EDITOR config.yaml
```

Основные поля:

| Поле | Описание |
|------|----------|
| `telegram.api_id` | Числовой API ID с my.telegram.org |
| `telegram.api_hash` | Строковый API Hash |
| `telegram.phone` | Номер телефона (запрашивается интерактивно, если не указан) |
| `channels` | Список `@username` или числовых ID чатов |
| `output.dir` | Директория для записи Markdown-файлов |
| `output.download_media` | Скачивать ли медиафайлы |
| `dump.incremental` | Загружать только посты новее последнего запуска |

Значения могут ссылаться на переменные окружения: `api_hash: "${TG_API_HASH}"`.

---

## Использование

```bash
# Использовать config.yaml в текущей директории (по умолчанию)
./tgch_dump

# Указать путь к конфигу явно
./tgch_dump config.yaml
./tgch_dump --config /path/to/config.yaml

# Включить подробное логирование
TGCH_DEBUG=1 ./tgch_dump
```

При первом запуске будет предложено ввести **код подтверждения**, отправленный на ваш аккаунт Telegram (и пароль двухфакторной аутентификации, если он установлен). Сессия сохраняется в `telegram.database_dir` и переиспользуется при последующих запусках.

---

## Структура выходных данных

```
output/
└── Название_Канала/
    ├── .state.json                        ← состояние инкрементального дампа (управляется автоматически)
    ├── 2025-03-15_00001234.md
    ├── 2025-03-15_00001235.md
    ├── attachments/
    │   ├── 00001235_photo_1.jpg
    │   ├── 00001235_video_1.mp4
    │   └── …
    └── …
```

### Формат Markdown-файла

```markdown
---
id: 1234
date: "2025-03-15T14:30:00Z"
author: Название Канала
telegram_url: https://t.me/channel_username/1234
views: 5230
forwards: 12
comments: 7
has_media: true
media:
  - type: photo
    file: "attachments/00001234_photo_1.jpg"
reactions:
  "👍": 42
  "❤️": 15
poll:
  question: Какой язык?
  type: regular
  is_anonymous: true
  is_closed: false
  options:
    - text: Go
      votes: 150
    - text: Rust
      votes: 120
---

Текст поста с **жирным**, *курсивом*, `кодом` и [ссылками](https://example.com).

![photo](attachments/00001234_photo_1.jpg)
```

---

## Инкрементальные обновления

При `dump.incremental: true` (значение по умолчанию) в каждой директории канала создаётся файл `.state.json`, отслеживающий максимальный обработанный ID сообщения. При повторном запуске загружаются только новые сообщения.

Для полного повторного дампа удалите `.state.json` или установите `dump.incremental: false`.

---

## Переменные окружения

| Переменная | Описание |
|------------|----------|
| `TGCH_DEBUG` | Установите любое непустое значение для включения debug-логирования |
| `TG_API_ID` | Можно использовать в config.yaml как `${TG_API_ID}` |
| `TG_API_HASH` | Можно использовать в config.yaml как `${TG_API_HASH}` |

---

## Лицензия

MIT — см. [LICENSE](LICENSE).
