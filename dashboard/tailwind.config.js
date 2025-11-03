/** @type {import('tailwindcss').Config} */
module.exports = {
  content: ['./app/**/*.{ts,tsx}', './components/**/*.{ts,tsx}'],
  theme: {
    extend: {
      fontFamily: {
        sans: ['Inter', 'ui-sans-serif', 'system-ui', 'sans-serif'],
      },
      boxShadow: {
        glow: '0 20px 45px -20px rgb(56 189 248 / 0.4)',
      },
      backgroundImage: {
        'radial-dusk':
          'radial-gradient(circle at 20% 20%, rgba(56, 189, 248, 0.25), transparent 60%), radial-gradient(circle at 80% 0%, rgba(236, 72, 153, 0.2), transparent 55%), radial-gradient(circle at 50% 80%, rgba(34, 197, 94, 0.2), transparent 60%)',
      },
    },
  },
  plugins: [require('@tailwindcss/forms')],
};
