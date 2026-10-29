(() => {
    const selected = document.getElementById('target-receiver');
    const input = document.getElementById('receiver-search');
    const list = document.getElementById('receiver-options');
    const clear = document.getElementById('clear-receiver');
    const status = document.getElementById('receiver-search-status');
    const picker = input.closest('.receiver-picker');
    let matches = [];
    let active = -1;

    function score(name, query) {
        const normalized = value => value.toLocaleLowerCase().replace(/[\s_-]+/g, '');
        const candidate = normalized(name);
        const search = normalized(query);
        if (!search) return 0;
        if (candidate === search) return 0;
        if (candidate.startsWith(search)) return 1;
        if (candidate.includes(search)) return 10 + candidate.indexOf(search);
        let position = -1;
        let gaps = 0;
        for (const character of search) {
            const next = candidate.indexOf(character, position + 1);
            if (next < 0) return Infinity;
            gaps += next - position - 1;
            position = next;
        }
        return 100 + gaps;
    }

    function close() {
        list.hidden = true;
        input.setAttribute('aria-expanded', 'false');
        input.removeAttribute('aria-activedescendant');
    }

    function highlight() {
        list.querySelectorAll('[role="option"]').forEach((option, index) => {
            option.setAttribute('aria-selected', String(index === active));
        });
        if (active >= 0) input.setAttribute('aria-activedescendant', `receiver-option-${active}`);
        else input.removeAttribute('aria-activedescendant');
    }

    function choose(name, focus = true) {
        selected.value = name;
        input.value = selected.value;
        input.setCustomValidity('');
        input.setAttribute('aria-invalid', 'false');
        clear.hidden = !input.value;
        if (focus) input.focus({preventScroll: true});
        close();
    }

    function render() {
        const ranked = Array.from(selected.options, option => option.value).filter(Boolean)
            .map(name => ({name, score: score(name, input.value)}))
            .filter(match => Number.isFinite(match.score))
            .sort((a, b) => a.score - b.score || a.name.localeCompare(b.name));
        matches = ranked.slice(0, 12).map(match => match.name);
        active = matches.length ? 0 : -1;
        list.replaceChildren();
        matches.forEach((name, index) => {
            const option = document.createElement('div');
            option.id = `receiver-option-${index}`;
            option.className = 'receiver-option';
            option.setAttribute('role', 'option');
            option.tabIndex = -1;
            option.textContent = name;
            option.addEventListener('mousedown', event => event.preventDefault());
            option.addEventListener('click', () => choose(name));
            list.appendChild(option);
        });
        if (!matches.length) {
            const empty = document.createElement('div');
            empty.className = 'receiver-empty';
            empty.textContent = 'No matching receivers';
            list.appendChild(empty);
        }
        status.textContent = ranked.length ? `${ranked.length} matching receivers. Use arrow keys and Enter to choose.` : 'No matching receivers.';
        list.hidden = false;
        input.setAttribute('aria-expanded', 'true');
        highlight();
    }

    input.addEventListener('focus', render);
    input.addEventListener('input', () => {
        const exact = Array.from(selected.options).find(option => option.value.toLocaleLowerCase() === input.value.trim().toLocaleLowerCase());
        selected.value = exact ? exact.value : '';
        input.setCustomValidity(input.value.trim() && !selected.value ? 'Choose a receiver from the matches, or clear this field.' : '');
        input.setAttribute('aria-invalid', 'false');
        clear.hidden = !input.value;
        render();
    });
    input.addEventListener('invalid', () => input.setAttribute('aria-invalid', 'true'));
    input.addEventListener('keydown', event => {
        if (event.key === 'Escape') {
            event.preventDefault();
            close();
        } else if (event.key === 'ArrowDown' || event.key === 'ArrowUp') {
            event.preventDefault();
            if (list.hidden) render();
            else if (matches.length) active = (active + (event.key === 'ArrowDown' ? 1 : -1) + matches.length) % matches.length;
            highlight();
            if (active >= 0) document.getElementById(`receiver-option-${active}`).scrollIntoView({block: 'nearest'});
        } else if (event.key === 'Enter' && !list.hidden) {
            event.preventDefault();
            if (active >= 0) choose(matches[active]);
        }
    });
    picker.addEventListener('focusout', event => {
        if (!picker.contains(event.relatedTarget)) close();
    });
    clear.addEventListener('click', () => choose(''));
    window.syncReceiverPicker = () => choose(selected.value, false);
    close();
})();
