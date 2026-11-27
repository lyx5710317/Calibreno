# Readers plugged in

A reader plugged into the computer is a library of its own: it is listed
in the library switcher, below the recent libraries, and choosing it shows
its books on the same shelf and list as the library's, with their own
search, sort and format filter. The library stays open behind it, to send
books from and to tell which of the reader's books it has.

Today: Kindles that connect as a USB drive (USB mass storage, the Kindles
before 2024), on Linux, macOS and Windows. The design takes MTP (Kindles
since 2024) and other readers (Kobo) without changing the app.

## What the user can do

- **See the reader's books**, with the covers calibre keeps for them, the
  free space in the switcher, and in each book's detail whether the
  library has it (by calibre uuid, else by title and first author, as
  calibre matches).
- **Send books** from the library's book menu (Send to Kindle), each in
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

## BOOX and WeRead

- For **BOOXDrop**, connect the computer and reader to the same network,
  open BOOXDrop on the reader, and enter the address it displays in
  Calibreno's **Settings → BOOX**. Test the connection and apply the
  address, then use **Send to BOOX** from the library's book menu. The
  app reports confirmed uploads and flags uncertain results so you can
  check the device before resending. The settings pane and eligible
  transfer errors offer the device's manual upload page.
  [BOOX transfer guide](https://help.boox.com/fa5fa/48481/36dc4)
- For **WeRead**, use **Import to WeRead…** on a library book. Calibreno
  opens the official shelf and reveals the book's EPUB. Choose
  **传书到手机** on the website, sign in if requested, and select the
  EPUB. If the book needs an EPUB, the app opens the conversion dialog
  with EPUB as the target.
  [WeRead shelf](https://weread.qq.com/web/shelf)

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

### Adding MTP (Kindles since 2024)

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
