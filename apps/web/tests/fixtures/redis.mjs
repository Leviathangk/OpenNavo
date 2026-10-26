import { createServer } from 'node:net';
import { once } from 'node:events';

// Local RESP fixture for the real ioredis/unstorage adapter. Only snapshot reads fail; Nitro's
// XML caches keep working, so the test catches an outage accidentally cached as a successful sitemap.
export async function redisFixture(t) {
  const values = new Map();
  const sockets = new Set();
  let failure = '';
  let failures = 0;
  const bulk = value => (value === undefined ? '$-1\r\n' : `$${Buffer.byteLength(value)}\r\n${value}\r\n`);
  const execute = args => {
    const [command, key, value] = args;
    switch (command.toLowerCase()) {
      case 'client':
        return '+OK\r\n';
      case 'info':
        return bulk('redis_version:7.0.0\r\nloading:0\r\n');
      case 'ping':
        return '+PONG\r\n';
      case 'get':
        if (failure && key.includes(failure)) {
          failures++;
          return '-ERR Command timed out\r\n';
        }
        return bulk(values.get(key));
      case 'mget':
        return `*${args.length - 1}\r\n${args
          .slice(1)
          .map(item => bulk(values.get(item)))
          .join('')}`;
      case 'set':
        values.set(key, value);
        return '+OK\r\n';
      case 'exists':
        return `:${values.has(key) ? 1 : 0}\r\n`;
      case 'del':
      case 'unlink':
        return `:${args.slice(1).filter(item => values.delete(item)).length}\r\n`;
      case 'quit':
        return '+OK\r\n';
      default:
        throw new Error(`Unsupported fixture Redis command: ${command}`);
    }
  };
  const server = createServer(socket => {
    sockets.add(socket);
    socket.on('close', () => sockets.delete(socket));
    let buffered = Buffer.alloc(0);
    socket.on('data', chunk => {
      buffered = Buffer.concat([buffered, chunk]);
      while (buffered.length) {
        const end = buffered.indexOf('\r\n');
        if (end < 0) return;
        const count = Number(buffered.subarray(1, end).toString());
        let offset = end + 2;
        const args = [];
        for (let i = 0; i < count; i++) {
          const sizeEnd = buffered.indexOf('\r\n', offset);
          if (sizeEnd < 0) return;
          const size = Number(buffered.subarray(offset + 1, sizeEnd).toString());
          offset = sizeEnd + 2;
          if (buffered.length < offset + size + 2) return;
          args.push(buffered.subarray(offset, offset + size).toString());
          offset += size + 2;
        }
        buffered = buffered.subarray(offset);
        socket.write(execute(args));
      }
    });
  });
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(async () => {
    for (const socket of sockets) socket.destroy();
    await new Promise(resolve => server.close(resolve));
  });
  return {
    url: `redis://127.0.0.1:${server.address().port}`,
    fail: part => {
      failure = part;
      failures = 0;
    },
    failures: () => failures
  };
}
