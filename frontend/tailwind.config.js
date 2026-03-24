/** @type {import('tailwindcss').Config} */
export default {
  content: [
    "./index.html",
    "./src/**/*.{js,ts,jsx,tsx}",
  ],
  theme: {
    extend: {
      fontFamily: {
        sans: [
          '-apple-system', 'BlinkMacSystemFont', '"SF Pro Display"', '"SF Pro Text"',
          '"Helvetica Neue"', 'Arial', 'sans-serif',
        ],
        mono: ['"SF Mono"', 'ui-monospace', 'SFMono-Regular', 'Menlo', 'monospace'],
      },
      colors: {
        apple: {
          blue: '#007AFF',
          green: '#34C759',
          red: '#FF3B30',
          orange: '#FF9500',
          yellow: '#FFCC00',
          purple: '#AF52DE',
          pink: '#FF2D55',
          teal: '#5AC8FA',
          indigo: '#5856D6',
          gray: {
            1: '#8E8E93',
            2: '#AEAEB2',
            3: '#C7C7CC',
            4: '#D1D1D6',
            5: '#E5E5EA',
            6: '#F2F2F7',
          },
        },
      },
      borderRadius: {
        'apple': '12px',
        'apple-lg': '16px',
        'apple-xl': '20px',
      },
      boxShadow: {
        'apple-sm': '0 1px 3px rgba(0, 0, 0, 0.04), 0 1px 2px rgba(0, 0, 0, 0.06)',
        'apple': '0 2px 8px rgba(0, 0, 0, 0.06), 0 1px 3px rgba(0, 0, 0, 0.04)',
        'apple-lg': '0 4px 16px rgba(0, 0, 0, 0.08), 0 2px 6px rgba(0, 0, 0, 0.04)',
      },
      fontSize: {
        'apple-title1': ['28px', { lineHeight: '34px', fontWeight: '700', letterSpacing: '0.36px' }],
        'apple-title2': ['22px', { lineHeight: '28px', fontWeight: '700', letterSpacing: '0.35px' }],
        'apple-title3': ['20px', { lineHeight: '25px', fontWeight: '600', letterSpacing: '0.38px' }],
        'apple-headline': ['17px', { lineHeight: '22px', fontWeight: '600', letterSpacing: '-0.41px' }],
        'apple-body': ['17px', { lineHeight: '22px', fontWeight: '400', letterSpacing: '-0.41px' }],
        'apple-callout': ['16px', { lineHeight: '21px', fontWeight: '400', letterSpacing: '-0.32px' }],
        'apple-subhead': ['15px', { lineHeight: '20px', fontWeight: '400', letterSpacing: '-0.24px' }],
        'apple-footnote': ['13px', { lineHeight: '18px', fontWeight: '400', letterSpacing: '-0.08px' }],
        'apple-caption1': ['12px', { lineHeight: '16px', fontWeight: '400' }],
        'apple-caption2': ['11px', { lineHeight: '13px', fontWeight: '400', letterSpacing: '0.07px' }],
      },
    },
  },
  plugins: [],
}
