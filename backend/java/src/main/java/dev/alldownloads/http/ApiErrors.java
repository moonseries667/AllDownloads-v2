package dev.alldownloads.http;

import dev.alldownloads.business.Failure;
import org.springframework.http.ResponseEntity;
import org.springframework.web.bind.annotation.*;
import org.springframework.http.converter.HttpMessageNotReadableException;
import org.springframework.web.bind.MissingRequestHeaderException;
import java.time.DateTimeException;
import java.util.Set;
import org.springframework.web.servlet.resource.NoResourceFoundException;
import static dev.alldownloads.business.Json.*;

@RestControllerAdvice
public class ApiErrors {
    @ExceptionHandler(Failure.class) public ResponseEntity<?> domain(Failure e) {return error(e.status,e.code,e.getMessage());}
    @ExceptionHandler(NoResourceFoundException.class) public ResponseEntity<?> missing(NoResourceFoundException e) {return error(404,"NOT_FOUND","此接口尚未提供");}
    @ExceptionHandler(dev.alldownloads.feedback2.DomainSql.DomainError.class) public ResponseEntity<?> feedback(dev.alldownloads.feedback2.DomainSql.DomainError e) {int status=e.code.equals("NOT_FOUND")?404:Set.of("INVALID_INPUT","EMPTY_SELECTION").contains(e.code)?400:409;return error(status,e.code,dev.alldownloads.business.OperationQueue.message(e.code));}
    @ExceptionHandler({IllegalArgumentException.class,DateTimeException.class,HttpMessageNotReadableException.class,MissingRequestHeaderException.class}) public ResponseEntity<?> bad(Exception e) {return error(400,"INVALID_INPUT","请求参数或必需版本字段不正确");}
    @ExceptionHandler(Exception.class) public ResponseEntity<?> failed(Exception e) {return error(500,"INTERNAL_ERROR","业务事务未完成，请稍后重试；未确认成功");}
    static ResponseEntity<?> error(int status,String code,String message) {return ResponseEntity.status(status).body(object("error",object("code",code,"message",message,"retryable",status>=500,"action",status==409?"REFRESH":null,"details",object()),"requestId",id()));}
}
