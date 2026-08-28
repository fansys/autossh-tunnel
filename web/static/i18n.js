/**
 * 国际化 (i18n) 引擎
 * 支持 简体中文 (zh) 与 English (en) 单击一键快速切换（带国旗 emoji）
 */
class I18n {
    constructor() {
        this.currentLang = 'zh';
        this.fallbackLang = 'en';
        this.storageKey = 'ssh-tunnel-lang';
        this.translations = {};
        this.isReady = false;
        this.supportedLanguages = [
            { code: 'zh', name: '简体中文', emoji: '🇨🇳' },
            { code: 'en', name: 'English', emoji: '🇺🇸' }
        ];

        this.loadLanguageFromStorage();
    }

    loadLanguageFromStorage() {
        const savedLang = localStorage.getItem(this.storageKey);
        if (savedLang === 'zh' || savedLang === 'en') {
            this.currentLang = savedLang;
        } else {
            // 自动读取浏览器首选语言
            const browserLang = (navigator.language || navigator.userLanguage || '').toLowerCase();
            this.currentLang = browserLang.startsWith('zh') ? 'zh' : 'en';
        }
    }

    saveLanguageToStorage() {
        localStorage.setItem(this.storageKey, this.currentLang);
    }

    async loadTranslations(lang) {
        if (this.translations[lang]) {
            return this.translations[lang];
        }

        try {
            const response = await fetch(`/static/locales/${lang}.json`);
            if (!response.ok) {
                throw new Error(`Failed to load ${lang}.json`);
            }
            const data = await response.json();
            this.translations[lang] = data;
            return data;
        } catch (error) {
            console.error(`Error loading translations for ${lang}:`, error);
            if (lang !== this.fallbackLang) {
                return await this.loadTranslations(this.fallbackLang);
            }
            return {};
        }
    }

    t(key, params = {}) {
        if (!key) return '';
        const keys = key.split('.');
        let value = this.translations[this.currentLang];

        // 尝试从当前语言获取
        for (const k of keys) {
            if (value && typeof value === 'object' && k in value) {
                value = value[k];
            } else {
                value = null;
                break;
            }
        }

        // 回退到默认语言
        if (value === null || value === undefined) {
            value = this.translations[this.fallbackLang];
            for (const fallbackKey of keys) {
                if (value && typeof value === 'object' && fallbackKey in value) {
                    value = value[fallbackKey];
                } else {
                    return key;
                }
            }
        }

        if (typeof value !== 'string') {
            return key;
        }

        // 变量插值替换 {{var}}
        return value.replace(/\{\{(\w+)\}\}/g, (match, paramKey) => {
            return params[paramKey] !== undefined ? params[paramKey] : match;
        });
    }

    async toggleLanguage() {
        const nextLang = (this.currentLang === 'zh') ? 'en' : 'zh';
        return this.switchLanguage(nextLang);
    }

    async setLanguage(lang) {
        return this.switchLanguage(lang);
    }

    async switchLanguage(lang) {
        if (lang !== 'zh' && lang !== 'en') {
            lang = 'en';
        }

        this.currentLang = lang;
        this.saveLanguageToStorage();

        // 确保翻译文件已加载
        await this.loadTranslations(lang);

        // 更新页面静态 DOM 文案与切换按钮显示
        this.updatePageContent();

        // 设置 HTML lang 属性
        document.documentElement.lang = lang;

        // 触发全局语言切换事件
        window.dispatchEvent(new CustomEvent('languageChanged', {
            detail: { language: lang }
        }));

        return true;
    }

    getCurrentLanguage() {
        return this.currentLang;
    }

    getSupportedLanguages() {
        return this.supportedLanguages;
    }

    async init() {
        if (this.isReady) {
            return true;
        }

        // 预加载中文和英文
        await Promise.all([
            this.loadTranslations('zh'),
            this.loadTranslations('en')
        ]);

        // 更新页面内容
        this.updatePageContent();
        this.bindToggleButtons();
        document.documentElement.lang = this.currentLang;
        this.isReady = true;

        window.dispatchEvent(new CustomEvent('i18nReady', {
            detail: { language: this.currentLang }
        }));

        return true;
    }

    bindToggleButtons() {
        document.querySelectorAll('#languageToggle, .language-toggle').forEach(btn => {
            btn.onclick = async (e) => {
                e.preventDefault();
                await this.toggleLanguage();
            };
        });
    }

    updatePageContent() {
        // 1. 更新所有带有 data-i18n 属性的元素
        document.querySelectorAll('[data-i18n]').forEach(element => {
            const key = element.getAttribute('data-i18n');
            const translation = this.t(key);

            if (element.tagName === 'INPUT' || element.tagName === 'TEXTAREA') {
                element.placeholder = translation;
            } else if (element.tagName === 'OPTION') {
                element.textContent = translation;
            } else {
                const icon = element.querySelector('.material-icons');
                if (icon) {
                    const textSpan = element.querySelector('span[data-i18n]');
                    if (textSpan) {
                        textSpan.textContent = translation;
                    } else {
                        element.innerHTML = `${icon.outerHTML} ${translation}`;
                    }
                } else {
                    element.textContent = translation;
                }
            }
        });

        // 2. 更新 data-i18n-placeholder
        document.querySelectorAll('[data-i18n-placeholder]').forEach(element => {
            const key = element.getAttribute('data-i18n-placeholder');
            element.placeholder = this.t(key);
        });

        // 3. 更新 data-i18n-title
        document.querySelectorAll('[data-i18n-title]').forEach(element => {
            const key = element.getAttribute('data-i18n-title');
            element.title = this.t(key);
        });

        // 4. 更新页面标题
        const titleKey = document.body.getAttribute('data-page-title');
        if (titleKey) {
            document.title = this.t(titleKey);
        }

        // 5. 更新所有语言切换按钮上的 emoji 和提示文本
        document.querySelectorAll('#languageToggle, .language-toggle').forEach(btn => {
            if (this.currentLang === 'zh') {
                // 当前是中文，按钮提示切换到英文
                btn.innerHTML = `<span class="text-xs font-bold tracking-wider flex items-center gap-1">🇺🇸 EN</span>`;
                btn.title = "Switch to English";
            } else {
                // 当前是英文，按钮提示切换到中文
                btn.innerHTML = `<span class="text-xs font-bold tracking-wider flex items-center gap-1">🇨🇳 中</span>`;
                btn.title = "切换至简体中文";
            }
        });
    }
}

// 全局单例
window.i18n = new I18n();

// DOMContentLoaded 时自初始化
document.addEventListener('DOMContentLoaded', async () => {
    await window.i18n.init();
});
