import type { Metadata, Viewport } from 'next';
import { Inter, JetBrains_Mono } from 'next/font/google';
import './globals.css';

// Clean geometric sans for UI/body text.
const sans = Inter({
  subsets: ['latin'],
  display: 'swap',
  variable: '--font-sans',
});

// Monospace for code, terminal accents, and CLI demos.
const mono = JetBrains_Mono({
  subsets: ['latin'],
  display: 'swap',
  variable: '--font-mono',
});

const SITE_URL = 'https://bharatcode.dev';
const TITLE = 'BharatCode — OpenCode for India';
const DESCRIPTION =
  'An open-source, Go-native terminal AI coding agent where your data stays in India. Local-first: run fully local (Ollama/LM Studio) or connect to open-weight and India-hosted models you choose. MIT-licensed, INR-aware, built for banks, enterprises, and DPDP-regulated teams.';

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: {
    default: TITLE,
    template: '%s — BharatCode',
  },
  description: DESCRIPTION,
  applicationName: 'BharatCode',
  keywords: [
    'BharatCode',
    'AI coding agent',
    'CLI coding agent',
    'open source',
    'Go',
    'MIT license',
    'open-weight models',
    'data sovereignty',
    'DPDP',
    'India',
    'Ollama',
    'LM Studio',
    'Kimi K2',
    'DeepSeek',
    'Qwen Coder',
    'terminal',
    'developer tools',
  ],
  authors: [{ name: 'BharatCode' }],
  creator: 'BharatCode',
  openGraph: {
    type: 'website',
    url: SITE_URL,
    siteName: 'BharatCode',
    title: TITLE,
    description: DESCRIPTION,
    locale: 'en_IN',
  },
  twitter: {
    card: 'summary_large_image',
    title: TITLE,
    description: DESCRIPTION,
  },
  alternates: {
    canonical: SITE_URL,
  },
  robots: {
    index: true,
    follow: true,
  },
};

export const viewport: Viewport = {
  themeColor: '#0B0C10',
  colorScheme: 'dark',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html
      lang="en"
      className={`${sans.variable} ${mono.variable} dark`}
      suppressHydrationWarning
    >
      <body className="min-h-screen bg-bg text-fg">{children}</body>
    </html>
  );
}
