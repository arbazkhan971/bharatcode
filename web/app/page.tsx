// BharatCode — landing page.
//
// The integrate phase wires every self-contained section component into a
// single scroll, top to bottom. Each section owns its own <Section> wrapper
// (gutters, max-width, anchor id), so this file is just composition + order.

import { Hero } from './components/sections/Hero';
import { Pillars } from './components/sections/Pillars';
import { Features } from './components/sections/Features';
import { Providers } from './components/sections/Providers';
import { Comparison } from './components/sections/Comparison';
import { Install } from './components/sections/Install';
import { Footer } from './components/sections/Footer';

export default function HomePage() {
  return (
    <>
      {/* `#top` is the Footer "back to top" anchor target. */}
      <main id="top">
        <Hero />
        <Pillars />
        <Features />
        <Providers />
        <Comparison />
        <Install />
      </main>
      <Footer />
    </>
  );
}
