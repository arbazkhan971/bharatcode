/** @type {import('next').NextConfig} */
const nextConfig = {
  // Static export — produces a fully static `out/` directory deployable to any
  // CDN / object storage. No server runtime required.
  output: 'export',
  // next/image optimization needs a server; disable it for static export.
  images: {
    unoptimized: true,
  },
  // Emit trailing-slash directories (e.g. /docs/ -> /docs/index.html) so the
  // export works cleanly on static hosts.
  trailingSlash: true,
  reactStrictMode: true,
};

module.exports = nextConfig;
