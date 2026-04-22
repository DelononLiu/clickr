const fs = require('fs');
const path = require('path');

const size = 64;
const channels = 4;
const data = Buffer.alloc(size * size * channels);

for (let y = 0; y < size; y++) {
  for (let x = 0; x < size; x++) {
    const idx = (y * size + x) * channels;
    const cx = size / 2;
    const cy = size / 2;
    const r = Math.sqrt((x - cx) ** 2 + (y - cy) ** 2);
    
    if (r < size / 2 - 2) {
      data[idx] = 0;
      data[idx + 1] = 217;
      data[idx + 2] = 255;
      data[idx + 3] = 255;
    } else if (r < size / 2) {
      data[idx] = 0;
      data[idx + 1] = 184;
      data[idx + 2] = 217;
      data[idx + 3] = 255;
    } else {
      data[idx + 3] = 0;
    }
  }
}

const pngSignature = Buffer.from([137, 80, 78, 71, 13, 10, 26, 10]);
const ihdr = createChunk('IHDR', Buffer.from([
  0, 0, 0, size,
  0, 0, 0, size,
  8, 6, 0, 0, 0
]));
const idat = createChunk('IDAT', require('zlib').deflateSync(data));
const iend = createChunk('IEND', Buffer.alloc(0));

function createChunk(type, data) {
  const length = Buffer.alloc(4);
  length.writeUInt32BE(data.length, 0);
  const typeBuffer = Buffer.from(type);
  const crc = crc32(Buffer.concat([typeBuffer, data]));
  const crcBuffer = Buffer.alloc(4);
  crcBuffer.writeUInt32BE(crc, 0);
  return Buffer.concat([length, typeBuffer, data, crcBuffer]);
}

function crc32(buffer) {
  let crc = 0xFFFFFFFF;
  const table = [];
  for (let i = 0; i < 256; i++) {
    let c = i;
    for (let j = 0; j < 8; j++) {
      c = (c & 1) ? (0xEDB88320 ^ (c >>> 1)) : (c >>> 1);
    }
    table[i] = c;
  }
  for (let i = 0; i < buffer.length; i++) {
    crc = table[(crc ^ buffer[i]) & 0xFF] ^ (crc >>> 8);
  }
  return crc ^ 0xFFFFFFFF;
}

const png = Buffer.concat([pngSignature, ihdr, idat, iend]);
fs.writeFileSync(path.join(__dirname, 'build/icon.png'), png);
console.log('Icon created: build/icon.png');