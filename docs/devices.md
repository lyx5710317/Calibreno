# 阅读设备与传输

在书架或列表右键一本书，也可以打开书籍详情的「更多」菜单，再选择「传输」。四个目的地的配置都在「设置 → 传送设置」。如果 Kindle、BOOX 或 KOReader 还没有可用的连接，从传输菜单可以直接进入对应设置。

| 目的地 | 传输方式与配置 |
| --- | --- |
| Kindle | 插入能识别为 USB 磁盘的 Kindle 后，可在「传输 → Kindle」选择 USB 直传。也可以在 Kindle 标签添加 Send to Kindle 收件地址和 SMTP 发件账户，再从同一菜单选择收件人。发件地址需要加入 Amazon 账户的认可发件人列表。[Amazon 说明](https://digprjsurvey.amazon.co.uk/csad/help/node/G7NECT4B4ZWHQ8WV) |
| 微信读书 | 在「传送设置 → 微信读书」打开专用 Chrome 或 Edge 窗口扫码登录。随后从单本书的「传输 → 微信读书 → 发送到微信读书」提交 EPUB。也可以打开官网手动导入。[微信读书书架](https://weread.qq.com/web/shelf) |
| BOOX | 电脑和 BOOX 接入同一网络。在设备上打开 BOOXDrop，把它显示的地址填到 BOOX 设置，测试连接并保存。设备未确认接收时，可先检查接收记录。直传不可用时，可以打开设备上传页手动传书。[BOOX 说明](https://help.boox.com/fa5fa/48481/36dc4) |
| KOReader | 电脑和设备接入同一网络。在 Calibreno 的 KOReader 标签启用无线设备连接，可配置端口和密码。在阅读器的 Calibre 插件中选择「连接」，再回到 Calibreno 的传输菜单发送。首次连接可能需要在设备上选择接收目录。[KOReader 说明](https://github.com/koreader/koreader/wiki/Calibre) |

KOReader 直传使用 Calibre 无线设备协议。工具栏的 OPDS 共享另提供书目浏览和按需下载，读者可以在设备上主动选择要下载的书。

## 微信读书传书

1. 在传送设置打开登录窗口，通过微信读书官网扫码。未显示二维码时，可以点击网页上的「登录」。扫码后回到 Calibreno 检查连接。专用窗口会保留登录会话，过期后可以再次扫码。
2. 选择一本书发送。Calibreno 会把书库里对应的 EPUB 交给官网上传表单。如果缺少 EPUB 且当前版本支持转换，先转换，再从完成提示点击「发送到微信读书」。
3. 最近传书记录会显示导入结果。遇到「待核对」，先打开微信读书查看书架或「我的文档」，找到书后再确认已导入。同一文件已导入或待核对时，重新发送需要再次确认。

官网上传表单不可用时，从书籍菜单选择「手动导入」。Calibreno 会打开官网并定位 EPUB，由你在网页选取这个文件。

## Readers plugged in

A reader plugged into the computer is a library of its own: it is listed
in the library switcher, below the recent libraries, and choosing it shows
its books on the same shelf and list as the library's, with their own
search, sort and format filter. The library stays open behind it, to send
books from and to tell which of the reader's books it has.

Calibreno supports Kindles that connect as a USB mass-storage drive on
Linux, macOS and Windows. Kindle models that use MTP still need an MTP
driver.

## What the user can do

- **See the reader's books**, with the covers calibre keeps for them, the
  free space in the switcher, and in each book's detail whether the
  library has it (by calibre uuid, else by title and first author, as
  calibre matches).
- **Send books** from the library's book menu (Transfer → Kindle), each in
  the first format the reader takes of those the book has (AZW3, MOBI,
  AZW, …; EPUB, FB2, CBZ and DJVU too on a Kindle with KOReader). Files can
  also be dropped on the reader's page.
- **Convert while sending to Kindle** on Windows x64. When the connected
  Kindle takes AZW3 and the library book has an EPUB but no format the
  device takes, Calibreno generates a temporary AZW3 and sends it. The
  first conversion downloads and verifies Epub3-Kindle 0.5.2. Later
  conversions can use the cached component offline. The library retains
  the original EPUB, and the temporary files are removed after the transfer.
- **Remove books** from the reader (book menu, detail, Delete key), after
  asking, with the notes and reading position the reader keeps for them.
  From the library's book menu, Remove from Kindle removes the copies of
  the books chosen.
- **Add the reader's books to the library**, with the metadata the reader
  has of them.
- **Eject** the reader from the toolbar (udisks on Linux, diskutil on
  macOS; on Windows, the system's Safely Remove Hardware).
- **Open a reader that isn't mounted** (Linux): a Kindle plugged in whose
  drive the desktop didn't mount is listed in the switcher as "Not
  mounted"; choosing it mounts it with udisks, as the file manager does,
  without a password on a desktop session, and shows its library. One
  that was ejected and stays plugged in is listed the same way; it is
  never mounted unless chosen.

## calibre compatibility

The reader's library is kept exactly as calibre keeps it, so a reader
goes from calibre to Calibreno and back with its library:

- `metadata.calibre`, at the reader's root, is calibre's record of the
  books: Calibreno reads it, adds the books it finds that calibre didn't
  record (reading their metadata and cover from the file), drops those
  whose file is gone, and adds the books it sends, all in calibre's JSON
  (its json_codec: every field it writes, dates in its format, "None" for
  an unknown date, thumbnails as `[width, height, base64]`). Entries are
  rewritten with the fields Calibreno doesn't know as they were.
- `driveinfo.calibre` keeps the reader's id (`device_store_uuid`), which
  Calibreno uses too; it records the connection and the library sending
  (`last_library_uuid`), as calibre does, keeping calibre's other fields.
- Books go where calibre puts them: its device save template
  `{author_sort}/{title} - {authors}` under `documents/`, each part an
  ASCII file name as calibre's `ascii_filename` makes it, shortened as its
  `shorten_components_to` does (tested against paths calibre wrote).
- Removing a book removes its `.sdr` folder and the Kindle's older
  sidecar files (`.apnx`, `.mbp`, …), and the author's folder when empty,
  as calibre's Kindle driver does.
- Sending a MOBI or AZW3 that names itself (EXTH ASIN and cdetype) writes
  its cover to `system/thumbnails/thumbnail_<asin>_<cdetype>_portrait.jpg`
  for the home screen, as calibre does. calibre also writes the ASIN and
  cdetype into files that lack them, so that the Kindle shows their
  covers; Calibreno doesn't modify book files yet.

As in calibre, opening a reader writes these two files: `driveinfo.calibre`
on every connection, `metadata.calibre` when the files changed since it
was written.

## Code

```
internal/device/          the layer: Storage, Source, Driver, Device, Watcher
  volume.go               Storage of a mounted folder (os.Root: never out of it)
  volumes_<os>.go         Source of the mounted USB drives; eject
  watch.go                polls the sources, opens what a driver matches
  usbms/                  calibre's library on a reader that is a storage
                          of files (its USBMS driver): metadata.calibre,
                          driveinfo.calibre, naming, scan, send, delete
  kindle/                 the Kindle driver, on usbms
internal/app/devices.go   the readers in the app: page state, send, remove
internal/app/view_devices.go  the page, its sidebar, toolbar, detail, menus
```

Three seams keep the next readers small:

- **A `device.Storage` is how files are reached**, with names relative to
  the reader's root. `device.Volume` is a mounted folder. `usbms` only
  uses the Storage (`LocalPath` is a shortcut, `""` when there is no
  path: files are then read through `Open`), so it runs unchanged on
  another kind of storage.
- **A `device.Source` lists the storages connected.** `device.Volumes`
  lists the mounted USB drives (on Linux with their USB ids from sysfs;
  `USBInfo.Protocol` is `"usbms"`). A `device.PendingSource` also lists
  the readers plugged in that it can't read yet, each with a `Connect`:
  on Linux, the USB partitions in `/sys/class/block` (or a whole disk
  without partitions) that `/proc/self/mounts` doesn't have, mounted with
  `udisksctl mount`. Drivers that are `device.USBMatcher`s recognise them
  by USB ids alone (the Kindle: Lab126's vendor id, as a USB drive), and
  the watcher offers them as `Available`; `Watcher.Connect` mounts one
  and returns it opened. An MTP reader will come in the same way: listed
  while not connected, connected when chosen.
- **A `device.Driver` recognises a reader and opens it as a
  `device.Device`**, the interface the app uses.

### Adding Kindle MTP support

1. Write an MTP `Storage` (over libusb through purego, keeping the build
   free of cgo, or over GNOME's gvfs mount on Linux as a first step), with
   `USB().Protocol = "mtp"` and `LocalPath` returning `""`.
2. Write a `Source` listing the MTP devices' storages, and add it to the
   watcher's sources in `App.startDevices`.
3. The Kindle driver matches by Lab126's vendor id and the `documents/`
   and `system/` folders, so it takes the MTP Kindle as it is, unless its
   storage differs (then give the driver a Config for it).

### Adding a Kobo

Write `internal/device/kobo`: a Driver matching `.kobo/KoboReader.sqlite`,
opening `usbms.Open` with Kobo's Config (books in the root, its formats,
KEPUB first), plus what Kobo needs beyond calibre's files: its shelves and
reading state are in `KoboReader.sqlite`. Add it to `App.deviceDrivers`.

## Not done yet

- Writing ASIN and cdetype into MOBI/AZW3 files sent, for their covers.
- Collections, annotations (`My Clippings.txt`), page numbers (APNX).
- The USB ids of drives on macOS and Windows (they are matched by what
  they hold), and offering readers that aren't mounted there (both mount
  drives by themselves; macOS could offer an ejected one with
  `diskutil mount`).
- The page's screens in the Figma file: they reuse the library's
  components, and need their design pass.
