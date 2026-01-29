/** @type {import('tailwindcss').Config} */
module.exports = {
    content: ["./static/**/*.{html,js}"],
    theme: {
        extend: {
            colors: {
                'kptv-dark': '#0a1929',
                'kptv-darker': '#050d17',
                'kptv-blue': '#1a5f8f',
                'kptv-blue-light': '#2d7bb3',
                'kptv-gray': '#1e2936',
                'kptv-gray-light': '#2d3a4a',
                'kptv-border': '#3a4a5e',
            }
        },
    },
    plugins: [],
}