export const chartTheme = {
  // Apple system colors
  color: [
    '#007AFF', '#34C759', '#FF9500', '#FF3B30', '#AF52DE',
    '#5AC8FA', '#FF2D55', '#5856D6', '#FFCC00', '#30D158',
    '#64D2FF', '#BF5AF2',
  ],
  backgroundColor: 'transparent',
  textStyle: {
    fontFamily: '-apple-system, BlinkMacSystemFont, "SF Pro Text", "Helvetica Neue", sans-serif',
    color: '#3C3C43',
    fontSize: 13,
  },
  title: {
    textStyle: {
      color: '#1C1C1E',
      fontSize: 17,
      fontWeight: 600,
      fontFamily: '-apple-system, BlinkMacSystemFont, "SF Pro Display", "Helvetica Neue", sans-serif',
    },
  },
  tooltip: {
    backgroundColor: 'rgba(255, 255, 255, 0.92)',
    borderColor: 'rgba(0, 0, 0, 0.06)',
    borderWidth: 1,
    textStyle: { color: '#1C1C1E', fontSize: 13 },
    extraCssText: 'backdrop-filter: blur(20px); border-radius: 10px; box-shadow: 0 4px 16px rgba(0,0,0,0.08);',
  },
};
