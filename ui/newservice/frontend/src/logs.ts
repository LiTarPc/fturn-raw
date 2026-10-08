export type LogLevel='debug'|'info'|'warn'|'error';
export type LogFilter='all'|'info'|'error';
export interface LogEntry {line:string;level:LogLevel;index:number}

// Read the leading level only: message payloads may themselves contain [ERROR].
export function parseLogEntry(line:string,index:number):LogEntry {
 const message=line.replace(/^(?:(?:\d{4}[/-]\d{2}[/-]\d{2}|\d{2}:\d{2}:\d{2}(?:\.\d+)?)\s+)+/,'');
 const token=message.match(/^\[(DEBUG|INFO|WARN|WARNING|ERROR|FATAL)\](?:\s|$)/i)?.[1].toUpperCase();
 let level:LogLevel='info';
 if(token==='DEBUG')level='debug';
 else if(token==='WARN'||token==='WARNING')level='warn';
 else if(token==='ERROR'||token==='FATAL'||/^(?:panic:|fatal error:)/i.test(message))level='error';
 return {line,level,index};
}
export function matchesLogFilter(entry:LogEntry,filter:LogFilter):boolean {
 return filter==='all'||(filter==='info'?entry.level==='info':entry.level==='warn'||entry.level==='error');
}
export const logLevelLabels:Record<LogLevel,string>={debug:'DEBUG',info:'INFO',warn:'WARN',error:'ERROR'};