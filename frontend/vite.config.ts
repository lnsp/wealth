import { defineConfig, type Plugin } from 'vite'
import react from '@vitejs/plugin-react'
import { readFileSync, writeFileSync } from 'fs'

// Plugin that copies sw.js to dist with build timestamp injected
function serviceWorkerPlugin(): Plugin {
  return {
    name: 'sw-version',
    writeBundle() {
      const swSrc = readFileSync('public/sw.js', 'utf-8')
      const buildId = Date.now().toString(36)
      const swOut = swSrc.replace(
        "const CACHE_NAME = 'finance-tracker-v1';",
        `const CACHE_NAME = 'finance-tracker-${buildId}';`
      )
      writeFileSync('dist/sw.js', swOut)
    },
  }
}

export default defineConfig({
  plugins: [react(), serviceWorkerPlugin()],
  server: {
    proxy: {
      '/api': 'http://localhost:8443',
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
})
