/* ==========================================================================
   SSH Tunnel Manager — Unified Theme Switcher (Tailwind & CSS Variables)
   Defaults to Dark Mode with Seamless Light Mode Toggle
   ========================================================================== */

(function() {
    // 1. Synchronous Initialization (Prevent Flash of wrong theme)
    var savedTheme = localStorage.getItem('ssh-tunnel-theme');
    if (savedTheme === 'light') {
        document.documentElement.classList.remove('dark');
        document.documentElement.setAttribute('data-theme', 'light');
    } else {
        // Default to dark mode
        document.documentElement.classList.add('dark');
        document.documentElement.setAttribute('data-theme', 'dark');
    }
})();

document.addEventListener('DOMContentLoaded', function() {
    var themeToggles = document.querySelectorAll('#themeToggle, .theme-toggle');

    function updateAllThemeIcons() {
        var isDark = document.documentElement.classList.contains('dark');
        themeToggles.forEach(function(btn) {
            var icon = btn.querySelector('.material-icons') || btn;
            if (icon) {
                icon.textContent = isDark ? 'dark_mode' : 'light_mode';
            }
            btn.title = isDark ? '切换到浅色主题' : '切换到深色主题';
        });
    }

    updateAllThemeIcons();

    themeToggles.forEach(function(btn) {
        btn.addEventListener('click', function(e) {
            e.preventDefault();
            var isDark = document.documentElement.classList.contains('dark');
            if (isDark) {
                document.documentElement.classList.remove('dark');
                document.documentElement.setAttribute('data-theme', 'light');
                localStorage.setItem('ssh-tunnel-theme', 'light');
            } else {
                document.documentElement.classList.add('dark');
                document.documentElement.setAttribute('data-theme', 'dark');
                localStorage.setItem('ssh-tunnel-theme', 'dark');
            }
            updateAllThemeIcons();
        });
    });
});
