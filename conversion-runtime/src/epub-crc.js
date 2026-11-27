import * as zlib from 'node:zlib';

const crcTable = zlib.crc32 ? undefined : Uint32Array.from({ length: 256 }, (_, value) => {
  for (let bit = 0; bit < 8; bit++) value = (value >>> 1) ^ (value & 1 ? 0xedb88320 : 0);
  return value;
});

export function epubCrc32(data, previous) {
  if (zlib.crc32) return zlib.crc32(data, previous);
  let crc = previous ^ -1;
  for (const byte of data) crc = (crc >>> 8) ^ crcTable[(crc ^ byte) & 255];
  return (crc ^ -1) >>> 0;
}
