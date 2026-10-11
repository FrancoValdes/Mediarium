# Usage: python web/tools/pt-pt-from-br.py web/src/locales/pt.json web/src/locales/pt-PT.json
# Run it after adding Brazilian Portuguese entries, then have a native speaker
# look over what changed. Entries fixed by hand in pt-PT.json are overwritten,
# so put lasting fixes into the rules below.
# Makes a European Portuguese catalogue from the Brazilian one: the common
# vocabulary differences, "a + infinitive" instead of the gerund after estar
# and in status labels, an article before possessives, and fewer "você".
# Placeholders like {time} are never touched. A native speaker reviews it.
import json
import re
import sys

src, dst = sys.argv[1], sys.argv[2]
d = json.load(open(src, encoding='utf-8'))

W = r'[A-Za-zÀ-ÿ]'

VOCAB = [
    # longer phrases first
    ('Encontra e salva', 'Encontra e guarda'),
    ('salva como', 'guardada como'),
    ('Busca automática', 'Pesquisa automática'),
    ('busca automática', 'pesquisa automática'),
    ('Indexadores e busca', 'Indexadores e pesquisa'),
    ('a busca', 'a pesquisa'),
    ('uma busca', 'uma pesquisa'),
    ('Busca imediata', 'Pesquisa imediata'),
    ('Busca e baixa', 'Procura e transfere'),
    ('Busca em segundo plano', 'Procura em segundo plano'),
    ('Busca no máximo', 'Procura no máximo'),
    ('Busca legendas', 'Procura legendas'),
    ('pôster', 'póster'), ('pôsteres', 'pósteres'), ('Pôsteres', 'Pósteres'), ('Pôster', 'Póster'),
    ('contêiner', 'contentor'), ('contêineres', 'contentores'), ('Contêiner', 'Contentor'),
    ('Polonês', 'Polaco'), ('Holandês', 'Neerlandês'),
    ('gênero', 'género'), ('gêneros', 'géneros'), ('Gênero', 'Género'), ('Gêneros', 'Géneros'),
    ('arquivo', 'ficheiro'), ('arquivos', 'ficheiros'), ('Arquivo', 'Ficheiro'), ('Arquivos', 'Ficheiros'),
    ('senha', 'palavra-passe'), ('senhas', 'palavras-passe'), ('Senha', 'Palavra-passe'), ('Senhas', 'Palavras-passe'),
    ('Configurações', 'Definições'), ('configurações', 'definições'),
    ('baixar', 'transferir'), ('Baixar', 'Transferir'), ('baixado', 'transferido'), ('baixados', 'transferidos'),
    ('baixada', 'transferida'), ('baixadas', 'transferidas'), ('baixe', 'transfira'), ('Baixe', 'Transfira'),
    ('baixa', 'transfere'), ('baixam', 'transferem'), ('baixou', 'transferiu'), ('baixando', 'transferindo'), ('Baixando', 'Transferindo'),
    ('usuário', 'utilizador'), ('usuários', 'utilizadores'), ('Usuário', 'Utilizador'), ('Usuários', 'Utilizadores'),
    ('usuária', 'utilizadora'),
    ('salvar', 'guardar'), ('Salvar', 'Guardar'), ('salvo', 'guardado'), ('salvos', 'guardados'), ('salva', 'guardada'),
    ('salvas', 'guardadas'), ('Salvo', 'Guardado'), ('salvando', 'guardando'), ('Salvando', 'Guardando'),
    ('excluir', 'eliminar'), ('Excluir', 'Eliminar'), ('excluído', 'eliminado'), ('excluída', 'eliminada'),
    ('excluídos', 'eliminados'), ('excluídas', 'eliminadas'), ('exclui', 'elimina'),
    ('celular', 'telemóvel'), ('celulares', 'telemóveis'), ('Celular', 'Telemóvel'),
    ('tela', 'ecrã'), ('telas', 'ecrãs'), ('Tela', 'Ecrã'),
    ('contato', 'contacto'), ('contatos', 'contactos'),
    ('cadastro', 'registo'), ('registro', 'registo'), ('registros', 'registos'), ('registrar', 'registar'),
    ('equipe', 'equipa'), ('aplicativo', 'aplicação'), ('aplicativos', 'aplicações'),
    ('buscar', 'procurar'), ('Buscar', 'Procurar'), ('buscando', 'procurando'), ('Buscando', 'Procurando'),
    ('busque', 'procure'), ('Busque', 'Procure'), ('busca', 'procura'), ('Busca', 'Procura'),
    ('rodar', 'correr'), ('rodando', 'correndo'), ('roda', 'corre'), ('rodam', 'correm'),
    ('buscado', 'procurado'), ('buscada', 'procurada'), ('buscados', 'procurados'), ('buscadas', 'procuradas'),
    ('você mesmo', 'você próprio'), ('Você mesmo', 'Você próprio'),
    ('Escaneando', 'Analisando'), ('escanear', 'analisar'), ('escaneia', 'analisa'),
    ('Gerenciar', 'Gerir'), ('gerenciar', 'gerir'), ('gerencia', 'gere'), ('Conferir', 'Verificar'), ('conferir', 'verificar'),
    ('mouse', 'rato'), ('detectar', 'detetar'), ('detectado', 'detetado'), ('detectada', 'detetada'),
    ('ótimo', 'ótimo'), ('fato', 'facto'),
    ('te lembrar', 'lembrar-lhe'), ('vai te', 'vai'), (' te ', ' lhe '),
]

AUX = r'(está|estão|estou|estamos|estava|estavam|esteja|estejam|estiver|estiverem|continua|continuam|continue|fica|ficam|ficou|segue|seguem|anda|andam|vai|vão|passa|passam)'
KEEP = {'quando', 'Quando', 'enquanto', 'Enquanto', 'mundo', 'fundo', 'segundo', 'segundos', 'Segundo', 'comando', 'comandos', 'lindo', 'brando', 'bando'}


def infinitive(g):
    for end, inf in (('ando', 'ar'), ('endo', 'er'), ('indo', 'ir')):
        if g.endswith(end):
            return g[: -len(end)] + inf
    return g


ART_BEFORE = r'(o|a|os|as|do|da|dos|das|no|na|nos|nas|ao|à|aos|às|pelo|pela|pelos|pelas|um|uma|este|esta|esse|essa|deste|desta|nesse|nessa)'
POSS = {'seu': 'o', 'sua': 'a', 'seus': 'os', 'suas': 'as'}
CONTRACT = {('em', 'o'): 'no', ('em', 'a'): 'na', ('em', 'os'): 'nos', ('em', 'as'): 'nas',
            ('de', 'o'): 'do', ('de', 'a'): 'da', ('de', 'os'): 'dos', ('de', 'as'): 'das',
            ('por', 'o'): 'pelo', ('por', 'a'): 'pela', ('por', 'os'): 'pelos', ('por', 'as'): 'pelas'}


def cap(word, like):
    return word[:1].upper() + word[1:] if like[:1].isupper() else word


def convert_part(s):
    s = s.replace('compartilh', 'partilh').replace('Compartilh', 'Partilh')
    for a, b in VOCAB:
        s = re.sub(rf'(?<!{W}){re.escape(a)}(?!{W})', b, s)
    # você: object forms first, then the subject is simply left out.
    s = re.sub(rf'(?<!{W})(para|a|de) você(?!{W})', lambda m: f'{m.group(1)} si', s)
    s = re.sub(rf'(?<!{W})com você(?!{W})', 'consigo', s)
    s = re.sub(rf'(?<!{W})Você ({W}+)', lambda m: m.group(1)[:1].upper() + m.group(1)[1:], s)
    s = re.sub(rf'(?<!{W})você (?!próprio)(?={W})', '', s)
    # estar + gerund -> estar a + infinitive
    s = re.sub(rf'(?<!{W}){AUX} ({W}+(?:ando|endo|indo))(?!{W})', lambda m: m.group(0) if m.group(2) in KEEP else f'{m.group(1)} a {infinitive(m.group(2))}', s)
    # a status label that starts with a gerund: "Procurando…" -> "A procurar…"
    s = re.sub(rf'^({W}+(?:ando|endo|indo))(?!{W})', lambda m: m.group(0) if m.group(1) in KEEP else 'A ' + infinitive(m.group(1)).lower(), s)
    s = re.sub(rf'([.!?:—] )({W}+(?:ando|endo|indo))(?!{W})', lambda m: m.group(0) if m.group(2) in KEEP or not m.group(2)[0].isupper() else m.group(1) + 'A ' + infinitive(m.group(2)).lower(), s)

    # an article before possessives: "sua biblioteca" -> "a sua biblioteca"
    def poss(m):
        before, prep, word = m.group(1), m.group(2), m.group(3)
        art = POSS[word.lower()]
        if prep:
            c = CONTRACT.get((prep.lower(), art))
            if c:
                return before + cap(c, prep) + ' ' + word.lower()
            return before + prep + ' ' + art + ' ' + word.lower()
        if word[0].isupper():
            return before + art.capitalize() + ' ' + word.lower()
        return before + art + ' ' + word

    s = re.sub(rf'(^|(?<!{W}))(?:(em|de|por|para|com|sobre|entre|sem|Em|De|Por|Para|Com) )?(seu|sua|seus|suas|Seu|Sua|Seus|Suas)(?!{W})', lambda m: m.group(0) if re.search(rf'(?<!{W}){ART_BEFORE} $', m.string[: m.start(3) - (len(m.group(2)) + 1 if m.group(2) else 0)]) else poss(m), s)
    return s


def convert(v):
    parts = re.split(r'(\{\w+\})', v)
    return ''.join(p if re.fullmatch(r'\{\w+\}', p) else convert_part(p) for p in parts)


out = {k: convert(v) for k, v in d.items()}
json.dump(dict(sorted(out.items())), open(dst, 'w', encoding='utf-8', newline='\n'), ensure_ascii=False, indent=2)
changed = sum(1 for k in d if d[k] != out[k])
print(f'{len(out)} entries, {changed} changed')
