import {useState} from 'react';
import {api,native} from '../api';
import type {Profile,SavedProfile} from '../api';
import {ScreenHeader} from './ScreenHeader';
type Props={entry?:SavedProfile;running:boolean;busy:boolean;invoke:(fn:()=>Promise<unknown>)=>Promise<boolean>;onSaved:()=>void;onBack:()=>void;setNotice:(s:string)=>void};
export function ProfileEditor({entry,running,busy,invoke,onSaved,onBack,setNotice}:Props){
 const [draft,setDraft]=useState<Profile>(entry?.profile??{Server:'',VkLink:'',Key:'',UplinkMode:'packet',Transport:'tcp',ObfProfile:'rtpopus2',Mtu:1420,Streams:10,StreamsPerCred:5,RouteMode:'full'});
 const [profileName,setProfileName]=useState(entry?.name??'');
 const creating=!entry;
 const viewing=running&&!creating;
 const locked=busy||viewing;
 const [importLink,setImportLink]=useState(''),[exportLink,setExportLink]=useState('');
 const patch=(p:Partial<Profile>)=>{setExportLink('');setDraft(d=>({...d,...p}))};
 const sharePanel=<details className="share-panel" open={viewing||undefined}><summary>Создать ссылку из этих настроек</summary>
     <p className="hint">Ссылка содержит ключ доступа. Передавай её только тому, кому разрешаешь подключаться.</p>
     <button className="action secondary" type="button" disabled={busy||!native} onClick={()=>void invoke(async()=>{setExportLink(await api.ExportProfile(draft))})}>Создать ссылку</button>
     {exportLink&&<><label>Ссылка подключения<textarea readOnly rows={3} value={exportLink} spellCheck={false} autoComplete="off" onFocus={e=>e.currentTarget.select()}/></label><button className="action secondary" type="button" disabled={busy} onClick={()=>void invoke(async()=>{await api.CopyConnectionLink(exportLink);setNotice('Ссылка скопирована.')})}>Копировать ссылку</button></>}
    </details>;
 return <form className="screen" onSubmit={e=>{e.preventDefault();if(locked)return;void invoke(async()=>{await api.SaveNamedProfile(draft,profileName,creating);onSaved();if(running&&creating)setNotice("Профиль добавлен. Текущее подключение продолжает работать.")})}}>
  <ScreenHeader title={creating?'Новый профиль':'Настройки профиля'} onBack={onBack}/>
  {viewing&&sharePanel}
    <label>Название профиля<input required maxLength={80} disabled={locked} value={profileName} onChange={e=>setProfileName(e.target.value)} placeholder="Например, Финляндия"/></label>
    {!viewing&&<section className="share-panel" aria-label="Импорт конфигурации">
     <label>Вставить ссылку конфигурации<textarea rows={2} disabled={busy} value={importLink} onChange={e=>setImportLink(e.target.value)} placeholder="fturnraw://…" autoComplete="off" spellCheck={false}/></label>
     <button className="action secondary" type="button" disabled={busy||!native||!importLink.trim()} onClick={()=>void invoke(async()=>{const p=await api.ImportProfile(importLink);setDraft(p);setImportLink('');setExportLink('');onSaved();if(running)setNotice('Профиль импортирован. Текущее подключение продолжает работать.')})}>Добавить профиль из ссылки</button>
     <p className="hint">Ссылка заполняет сервер, VK-звонок, ключ, MTU, потоки, транспорт и профиль RTP.</p>
    </section>}
    <p className="hint">TCP или UDP через TURN. Внешнее соединение выбирается автоматически: активный VPN, Ethernet или Wi-Fi.</p>
    <label>Сервер IPv4:порт<input required disabled={locked} value={draft.Server} onChange={e=>patch({Server:e.target.value})} placeholder="192.0.2.1:56010"/></label>
    <label>Ссылка VK-звонка<input type="url" required disabled={locked} value={draft.VkLink} onChange={e=>patch({VkLink:e.target.value})} placeholder="https://vk.ru/call/join/…"/></label>
    <label>Raw key<textarea required rows={3} disabled={locked} value={draft.Key??''} onChange={e=>patch({Key:e.target.value,KeyFile:''})} placeholder="64 шестнадцатеричных символа" autoComplete="off" spellCheck={false}/></label>
    <p className="hint">Ключ хранится внутри профиля и входит в ссылку подключения. Отдельный файл не нужен.</p>
    <div className="row"><label>Транспорт TURN<select disabled={locked} value={draft.Transport??'tcp'} onChange={e=>patch({Transport:e.target.value as Profile['Transport']})}><option value="tcp">TCP</option><option value="udp">UDP</option></select></label><label>Профиль RTP<select disabled={locked} value={draft.ObfProfile??'rtpopus2'} onChange={e=>patch({ObfProfile:e.target.value as Profile['ObfProfile']})}><option value="rtpopus2">rtpopus2</option><option value="rtpopus3">rtpopus3</option></select></label></div>
    <p className="hint">rtpopus3 требует обновлённый сервер с поддержкой этого профиля. Изменения применяются при следующем подключении.</p>
    <label>Исходящий TCP<select disabled={locked} value={draft.UplinkMode??'packet'} onChange={e=>patch({UplinkMode:e.target.value as Profile['UplinkMode']})}><option value="packet">По пакетам — прежний режим</option><option value="tcp-flow">По соединениям — экспериментальный</option></select></label>
    <p className="hint">В режиме по соединениям каждый исходящий TCP-поток использует один TURN-канал. Это уменьшает перестановки между каналами, но скорость одного соединения ограничена его каналом. UDP и загрузка с сервера работают по-прежнему. Применяется после переподключения.</p>
    <div className="row"><label>MTU<input type="number" required min={576} max={1500} disabled={locked} value={draft.Mtu} onChange={e=>patch({Mtu:Number(e.target.value)})}/></label><label>Потоки<input type="number" required min={1} max={64} disabled={locked} value={draft.Streams} onChange={e=>patch({Streams:Number(e.target.value)})}/></label></div>
    <label>Потоков на реквизиты VK (Streams/cred)<input type="number" required min={1} max={64} disabled={locked} value={draft.StreamsPerCred} onChange={e=>patch({StreamsPerCred:Number(e.target.value)})}/></label>
    <p className="hint">Сколько TURN-соединений используют один набор реквизитов VK. По умолчанию — 5.{draft.Streams>0&&draft.StreamsPerCred>0&&<> Групп авторизации: {Math.ceil(draft.Streams/draft.StreamsPerCred)}.</>}</p>
    <label>Трафик<select disabled={locked} value={draft.RouteMode} onChange={e=>patch({RouteMode:e.target.value})}><option value="full">Весь IPv4 через Raw</option><option value="tunnel">Только соединение с сервером</option></select></label>
    <p className="hint">В полном режиме IPv6 блокируется, DNS отправляется через Raw. Отключение возвращает прежние сетевые настройки. MTU должен совпадать с сервером.</p>
    {running&&<p className="notice">{creating?"Новый профиль сохранится в списке. Текущее подключение останется на выбранном сервере.":"Соединение активно. Параметры доступны только для просмотра. Ссылку можно создать и скопировать выше."}</p>}
    {!viewing&&<button className="action" disabled={locked} type="submit">Сохранить</button>}
    {!viewing&&sharePanel}


 </form>
}
