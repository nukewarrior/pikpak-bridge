package worker

import (
    "encoding/json"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "strings"

    "github.com/nukewarrior/pikpak-bridge/internal/domain"
)

type promotionFile struct {
    Relative string \`json:"relative"\`
    HadOld bool \`json:"had_old"\`
}

// PromoteLocalDownloads promotes a repeat transfer after every staging file
// has passed aria2 and local size checks. Original files are backed up on the
// same filesystem and can be restored after a crash before the commit marker.
// Per-file renames are atomic; a multi-file set is not globally atomic.
func PromoteLocalDownloads(localDir, taskID string, downloads []domain.Download) error {
    if localDir=="" || !filepath.IsAbs(localDir) {
        return errors.New("必须配置 Bridge 可访问的 NAS 本地挂载目录 local_dir")
    }
    if taskID=="" || strings.ContainsAny(taskID,"/\\") {
        return errors.New("invalid task ID")
    }
    base,err:=filepath.EvalSymlinks(localDir)
    if err!=nil {return fmt.Errorf("NAS 挂载目录不可访问: %w",err)}
    if fi,err:=os.Stat(base);err!=nil || !fi.IsDir() {
        return errors.New("NAS 挂载目录不存在或不是目录")
    }
    stage:=filepath.Join(base,".pikpak-bridge-staging",taskID)
    backupDir:=filepath.Join(base,".pikpak-bridge-backups",taskID)
    journal:=filepath.Join(stage,".bridge-promoting.json")
    committed:=filepath.Join(stage,".bridge-committed")
    if err:=safePath(base,stage);err!=nil {return err}
    if err:=safePath(base,backupDir);err!=nil {return err}
    if _,err:=os.Stat(committed);err==nil {return nil}
    if journalContent,err:=os.ReadFile(journal);err==nil {
        var oldPlan []promotionFile
        if err:=json.Unmarshal(journalContent,&oldPlan);err!=nil {return fmt.Errorf("无法解析上次替换日志: %w",err)}
        if err:=restorePromotion(base,stage,backupDir,oldPlan);err!=nil {
            return fmt.Errorf("上次替换中断，原文件恢复失败；备份仍在 %s: %w",backupDir,err)
        }
        if err:=os.Remove(journal);err!=nil {return err}
    } else if !errors.Is(err,os.ErrNotExist) {return err}

    plan:=make([]promotionFile,0,len(downloads))
    visited:=map[string]bool{}
    for _,d:=range downloads {
        rel:=filepath.Clean(filepath.FromSlash(d.RelativePath))
        if !validPromoteRelative(rel) {return fmt.Errorf("不安全的相对路径: %q",d.RelativePath)}
        lower:=strings.ToLower(rel)
        if visited[lower] {return fmt.Errorf("重复的目标路径: %q",d.RelativePath)}
        visited[lower]=true
        src:=filepath.Join(stage,rel)
        dst:=filepath.Join(base,rel)
        bak:=filepath.Join(backupDir,rel)
        for _,target:=range []string{src,dst,bak} {
            if err:=safePath(base,target);err!=nil {return err}
        }
        fi,err:=os.Lstat(src)
        if err!=nil {return fmt.Errorf("临时下载文件不存在 %s: %w",rel,err)}
        if !fi.Mode().IsRegular() || fi.Size()!=d.TotalLength {
            return fmt.Errorf("临时文件大小或类型不符 %s: got %d expected %d",rel,fi.Size(),d.TotalLength)
        }
        old:=false
        if fi,err:=os.Lstat(dst);err==nil {
            if !fi.Mode().IsRegular() {return fmt.Errorf("现有目标不是普通文件: %s",dst)}
            old=true
        } else if !errors.Is(err,os.ErrNotExist) {return err}
        if _,err:=os.Lstat(bak);err==nil {
            return fmt.Errorf("发现上次遗留备份，请先核查 %s",bak)
        } else if !errors.Is(err,os.ErrNotExist) {return err}
        plan=append(plan,promotionFile{Relative:rel,HadOld:old})
    }
    body,err:=json.Marshal(plan)
    if err!=nil {return err}
    if err:=durableMarker(journal,body);err!=nil {return err}
    rollback:=func(cause error) error {
        if undoErr:=restorePromotion(base,stage,backupDir,plan);undoErr!=nil {
            return errors.Join(cause,fmt.Errorf("回滚失败，备份位于 %s: %w",backupDir,undoErr))
        }
        _=os.Remove(journal)
        return cause
    }
    for _,item:=range plan {
        src:=filepath.Join(stage,item.Relative)
        dst:=filepath.Join(base,item.Relative)
        bak:=filepath.Join(backupDir,item.Relative)
        if err:=os.MkdirAll(filepath.Dir(dst),0755);err!=nil {return rollback(err)}
        if err:=safePath(base,dst);err!=nil {return rollback(err)}
        if item.HadOld {
            if err:=os.MkdirAll(filepath.Dir(bak),0700);err!=nil {return rollback(err)}
            if err:=safePath(base,bak);err!=nil {return rollback(err)}
            if err:=os.Rename(dst,bak);err!=nil {return rollback(err)}
        }
        if err:=os.Rename(src,dst);err!=nil {return rollback(err)}
    }
    if err:=durableMarker(committed,[]byte("complete"));err!=nil {return rollback(err)}
    _=os.Remove(journal)
    // Backups are only cleared after a durable commit marker exists.
    _=os.RemoveAll(backupDir)
    return nil
}

func validPromoteRelative(rel string) bool {
    if rel=="." || rel==".." || filepath.IsAbs(rel) ||
        strings.HasPrefix(rel,".."+string(filepath.Separator)) {return false}
    first:=strings.Split(rel,string(filepath.Separator))[0]
    if first==".pikpak-bridge-staging" || first==".pikpak-bridge-backups" {return false}
    return true
}

func restorePromotion(base,stage,backups string,plan []promotionFile) error {
    for i:=len(plan)-1;i>=0;i-- {
        item:=plan[i]
        if !validPromoteRelative(item.Relative) {return errors.New("corrupted promotion journal")}
        src:=filepath.Join(stage,item.Relative)
        dst:=filepath.Join(base,item.Relative)
        bak:=filepath.Join(backups,item.Relative)
        for _,p:=range []string{src,dst,bak} {
            if err:=safePath(base,p);err!=nil {return err}
        }
        _,srcErr:=os.Lstat(src)
        _,dstErr:=os.Lstat(dst)
        _,bakErr:=os.Lstat(bak)
        srcExists:=srcErr==nil
        dstExists:=dstErr==nil
        bakExists:=bakErr==nil
        if item.HadOld {
            if bakExists {
                if dstExists {
                    if srcExists {return fmt.Errorf("ambiguous restored state: %s",dst)}
                    if err:=os.Rename(dst,src);err!=nil {return err}
                }
                if err:=os.MkdirAll(filepath.Dir(dst),0755);err!=nil {return err}
                if err:=os.Rename(bak,dst);err!=nil {return err}
            } else if !srcExists || !dstExists {
                return fmt.Errorf("backup missing during recovery: %s",item.Relative)
            }
        } else {
            if !srcExists && dstExists {
                if err:=os.Rename(dst,src);err!=nil {return err}
            } else if !srcExists && !dstExists {
                return fmt.Errorf("staged file missing during recovery: %s",item.Relative)
            }
        }
    }
    return nil
}

func durableMarker(path string,data []byte) error {
    f,err:=os.OpenFile(path,os.O_CREATE|os.O_WRONLY|os.O_TRUNC,0600)
    if err!=nil {return err}
    if _,err=f.Write(data);err!=nil {f.Close();return err}
    if err=f.Sync();err!=nil {f.Close();return err}
    return f.Close()
}

func safePath(base,target string) error {
    rel,err:=filepath.Rel(base,target)
    if err!=nil || rel==".." || strings.HasPrefix(rel,".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
        return fmt.Errorf("path escapes NAS root: %s",target)
    }
    current:=base
    for _,part:=range strings.Split(rel,string(filepath.Separator)) {
        if part=="" || part=="." {continue}
        current=filepath.Join(current,part)
        fi,err:=os.Lstat(current)
        if errors.Is(err,os.ErrNotExist) {continue}
        if err!=nil {return err}
        if fi.Mode()&os.ModeSymlink!=0 {return fmt.Errorf("refuse symlink path: %s",current)}
    }
    return nil
}
